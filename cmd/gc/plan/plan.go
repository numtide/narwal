package plan

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
	"github.com/spf13/viper"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
)

//nolint:gochecknoglobals
var (
	cfg *config.GC

	// s3 client will be used when we implement deletions.
	s3 *awssdk.BucketClient
	pg *pgxpool.Pool
)

type planSummary struct {
	completed  int32
	incomplete int32
	total      int32
}

func Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                "plan",
		Short:              "Plan and execute GC plans",
		PersistentPreRunE:  preRunE,
		PersistentPostRunE: postRunE,
	}

	cmd.AddCommand(planCreate())
	cmd.AddCommand(planList())
	cmd.AddCommand(planRemove())
	cmd.AddCommand(planApply())

	return cmd
}

func preRunE(cmd *cobra.Command, _ []string) error {
	var err error

	// parse viper into our config object
	if err = config.FromViper(viper.GetViper(), &cfg); err != nil {
		return fmt.Errorf("failed to create config from viper: %w", err)
	}

	if err = cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	log.Info("config loaded", "config_file", viper.ConfigFileUsed())

	// connect to postgres
	pg, err = cfg.Postgres.Connect(cmd.Context(), false)
	if err != nil {
		//nolint:wrapcheck
		return err
	}

	// connect to s3
	if s3, err = cfg.S3.Connect(cmd.Context()); err != nil {
		//nolint:wrapcheck
		return err
	}

	return nil
}

func postRunE(cmd *cobra.Command, _ []string) error {
	pg.Close()
	return nil
}

func tableName(planID int32, name string) string {
	return fmt.Sprintf("gc_plan_%d_%s", planID, name)
}

func closureTableName(planID int32) string {
	return tableName(planID, "closure")
}

func deletionsTableName(planID int32) string {
	return tableName(planID, "deletions")
}

func summarizePlan(ctx context.Context, conn *pgxpool.Conn, planID int32) (*planSummary, error) {
	result := &planSummary{}

	err := conn.QueryRow(
		ctx, `
		select 
		    count(applied_at) as completed, 
		    count(error) as incomplete,
		    count(*) as total
		from `+deletionsTableName(planID),
	).Scan(&result.completed, &result.incomplete, &result.total)
	if err != nil {
		return nil, fmt.Errorf("failed to summarize plan: %w", err)
	}

	return result, nil
}

func addDeletionsTable(ctx context.Context, tx pgx.Tx, planID int32) error {
	name := deletionsTableName(planID)

	// we might be re-running a plan so we only create the table if it doesn't exist
	_, err := tx.Exec(ctx, fmt.Sprintf(
		`
		create table if not exists %s (		     
			path varchar(128) primary key,		
			applied_at timestamp,
		    error varchar(256)
		)`,
		name,
	))
	if err != nil {
		return fmt.Errorf("failed to add deletions table: %w", err)
	}

	// truncate any entries from a previous run
	if _, err = tx.Exec(ctx, "truncate table "+name); err != nil {
		return fmt.Errorf("failed to truncate deletions table: %w", err)
	}

	return nil
}

func removePlanTables(ctx context.Context, tx pgx.Tx, planID int32) error {
	if _, err := tx.Exec(ctx, `drop table `+closureTableName(planID)); err != nil {
		return fmt.Errorf("failed to remove closure table: %w", err)
	}

	if _, err := tx.Exec(ctx, `drop table `+deletionsTableName(planID)); err != nil {
		return fmt.Errorf("failed to remove deletions table: %w", err)
	}

	return nil
}
