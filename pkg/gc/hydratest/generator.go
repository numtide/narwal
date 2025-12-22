package hydratest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"hash/fnv"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/queries"
	"golang.org/x/sync/errgroup"
)

// Config holds the configuration for test data generation.
type Config struct {
	NumProjects          int // Number of projects to create (default: 10)
	MinJobsetsPerProject int // Minimum jobsets per project (default: 3)
	MaxJobsetsPerProject int // Maximum jobsets per project (default: 10)
	MinBuildsPerJS       int // Minimum builds per jobset (default: 50)
	MaxBuildsPerJS       int // Maximum builds per jobset (default: 300)
	MinStepsPerBuild     int // Minimum build steps per build (default: 1)
	MaxStepsPerBuild     int // Maximum build steps per build (default: 5)
	MinOutputsPerStep    int // Minimum outputs per step (default: 1)
	MaxOutputsPerStep    int // Maximum outputs per step (default: 3)
	SuccessRate          int // Percentage of successful builds (default: 85)
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		NumProjects:          10,
		MinJobsetsPerProject: 3,
		MaxJobsetsPerProject: 10,
		MinBuildsPerJS:       50,
		MaxBuildsPerJS:       300,
		MinStepsPerBuild:     1,
		MaxStepsPerBuild:     5,
		MinOutputsPerStep:    1,
		MaxOutputsPerStep:    3,
		SuccessRate:          85,
	}
}

// Generator generates test data for the Hydra database.
type Generator struct {
	tb      testing.TB
	rng     *rand.Rand
	queries *queries.Queries
	config  Config

	// Bulk data collectors
	builds      []queries.CopyBuildsParams
	buildSteps  []queries.CopyBuildStepsParams
	stepOutputs []queries.CopyBuildStepOutputsParams
	nextBuildID int32

	// S3 client for uploading narinfo/nar files
	bucketClient *awssdk.BucketClient
}

func Generate(tb testing.TB, queries *queries.Queries, bucketClient *awssdk.BucketClient) {
	tb.Helper()
	NewGenerator(tb, queries, bucketClient).Generate()
}

// NewGenerator creates a new Generator with a seed derived from the test name.
// This provides reproducible, deterministic test data per test.
func NewGenerator(tb testing.TB, queries *queries.Queries, bucketClient *awssdk.BucketClient) *Generator {
	tb.Helper()

	// Use FNV hash of test name for a deterministic seed
	h := fnv.New64a()
	if _, err := h.Write([]byte(tb.Name())); err != nil {
		tb.Fatalf("failed to hash test name: %v", err)
	}

	seed := int64(h.Sum64()) //nolint:gosec

	tb.Logf("Using seed %d (from test name: %s)", seed, tb.Name())

	return &Generator{
		tb:           tb,
		rng:          rand.New(rand.NewSource(seed)), //nolint:gosec
		queries:      queries,
		config:       DefaultConfig(),
		nextBuildID:  1, // Build IDs start at 1 in fresh DB
		bucketClient: bucketClient,
	}
}

// Generate creates all test data in the database.
func (g *Generator) Generate() {
	tb := g.tb

	tb.Helper()

	start := time.Now()

	tb.Logf("Starting test data generation: %d projects, %d-%d jobsets/project, %d-%d builds/jobset",
		g.config.NumProjects,
		g.config.MinJobsetsPerProject, g.config.MaxJobsetsPerProject,
		g.config.MinBuildsPerJS, g.config.MaxBuildsPerJS)

	// Create users (small, use individual inserts)
	users := g.createUsers(tb)
	tb.Logf("Created %d users", len(users))

	// Shuffle package names and pick for projects
	packages := make([]string, len(PackageNames))
	copy(packages, PackageNames)
	g.rng.Shuffle(len(packages), func(i, j int) {
		packages[i], packages[j] = packages[j], packages[i]
	})

	// Pre-allocate slices for bulk data (estimate sizes)
	estimatedBuilds := g.config.NumProjects * g.config.MaxJobsetsPerProject * g.config.MaxBuildsPerJS
	estimatedSteps := estimatedBuilds * g.config.MaxStepsPerBuild
	estimatedOutputs := estimatedSteps * g.config.MaxOutputsPerStep

	g.builds = make([]queries.CopyBuildsParams, 0, estimatedBuilds)
	g.buildSteps = make([]queries.CopyBuildStepsParams, 0, estimatedSteps)
	g.stepOutputs = make([]queries.CopyBuildStepOutputsParams, 0, estimatedOutputs)

	// Phase 1: Generate all data in memory
	tb.Log("Phase 1: Generating data in memory...")

	var totalJobsets int

	for i := range g.config.NumProjects {
		if i >= len(packages) {
			break
		}

		owner := users[g.rng.Intn(len(users))]
		pkgName := packages[i]

		project := g.createProject(tb, pkgName, owner)

		// Create jobsets for this project
		numJobsets := g.randRange(g.config.MinJobsetsPerProject, g.config.MaxJobsetsPerProject)
		jobsetNames := g.pickRandomJobsetNames(numJobsets)

		for _, jsName := range jobsetNames {
			jobset := g.createJobset(tb, jsName, project.Name)

			// Generate builds for this jobset (collected in memory)
			numBuilds := g.randRange(g.config.MinBuildsPerJS, g.config.MaxBuildsPerJS)
			g.generateBuildsForJobset(jobset, pkgName, numBuilds)
		}

		totalJobsets += numJobsets

		if (i+1)%10 == 0 || i+1 == g.config.NumProjects {
			tb.Logf("Generated data for project %d/%d: %s", i+1, g.config.NumProjects, pkgName)
		}
	}

	tb.Logf("Phase 1 complete: %d builds, %d steps, %d outputs in memory",
		len(g.builds), len(g.buildSteps), len(g.stepOutputs))

	// Phase 2: Bulk insert using COPY protocol
	tb.Log("Phase 2: Bulk inserting with COPY protocol...")
	ctx := tb.Context()

	tb.Logf("Inserting %d builds...", len(g.builds))

	buildCount, err := g.queries.CopyBuilds(ctx, g.builds)
	if err != nil {
		tb.Fatalf("failed to bulk insert builds: %v", err)
	}

	tb.Logf("Inserted %d builds, now inserting %d build steps...", buildCount, len(g.buildSteps))

	stepCount, err := g.queries.CopyBuildSteps(ctx, g.buildSteps)
	if err != nil {
		tb.Fatalf("failed to bulk insert build steps: %v", err)
	}

	tb.Logf("Inserted %d steps, now inserting %d outputs...", stepCount, len(g.stepOutputs))

	outputCount, err := g.queries.CopyBuildStepOutputs(ctx, g.stepOutputs)
	if err != nil {
		tb.Fatalf("failed to bulk insert step outputs: %v", err)
	}

	dbElapsed := time.Since(start)
	tb.Logf("Database population complete in %s: %d projects, %d jobsets, %d builds, %d steps, %d outputs",
		dbElapsed.Round(time.Millisecond),
		g.config.NumProjects, totalJobsets, buildCount, stepCount, outputCount)

	// Phase 3: Upload narinfo and NAR files to S3
	g.uploadToS3(ctx)

	elapsed := time.Since(start)
	tb.Logf("Generation complete in %s", elapsed.Round(time.Millisecond))

	// Clear slices to free memory
	g.builds = nil
	g.buildSteps = nil
	g.stepOutputs = nil
}

func (g *Generator) createUsers(tb testing.TB) []string {
	tb.Helper()

	ctx := tb.Context()
	users := make([]string, 0, len(UserNames))

	for _, name := range UserNames {
		_, err := g.queries.CreateUser(ctx, queries.CreateUserParams{
			Username:     name,
			Fullname:     pgtype.Text{String: name + " User", Valid: true},
			Emailaddress: name + "@example.com",
			Password:     "!", // Disabled password
		})
		if err != nil {
			tb.Fatalf("failed to create user %s: %v", name, err)
		}

		users = append(users, name)
	}

	return users
}

func (g *Generator) createProject(tb testing.TB, name, owner string) queries.Project {
	tb.Helper()

	project, err := g.queries.CreateProject(tb.Context(), queries.CreateProjectParams{
		Name:        name,
		Displayname: name,
		Description: pgtype.Text{String: "Test project for " + name, Valid: true},
		Owner:       owner,
	})
	if err != nil {
		tb.Fatalf("failed to create project %s: %v", name, err)
	}

	return project
}

func (g *Generator) createJobset(tb testing.TB, name, project string) queries.Jobset {
	tb.Helper()

	jobset, err := g.queries.CreateJobset(tb.Context(), queries.CreateJobsetParams{
		Name:         name,
		Project:      project,
		Description:  pgtype.Text{String: name + " jobset", Valid: true},
		Nixexprinput: pgtype.Text{String: "src", Valid: true},
		Nixexprpath:  pgtype.Text{String: "release.nix", Valid: true},
	})
	if err != nil {
		tb.Fatalf("failed to create jobset %s/%s: %v", project, name, err)
	}

	return jobset
}

// generateBuildsForJobset generates build data and collects it in memory.
func (g *Generator) generateBuildsForJobset(
	jobset queries.Jobset,
	pkgName string,
	numBuilds int,
) {
	now := time.Now()
	thirtyDaysAgo := now.AddDate(0, 0, -30)

	for range numBuilds {
		buildID := g.nextBuildID
		g.nextBuildID++

		// Random timestamp in the last 30 days
		timestamp := thirtyDaysAgo.Add(time.Duration(g.rng.Int63n(int64(30 * 24 * time.Hour))))

		// Pick random version and system
		version := PackageVersions[g.rng.Intn(len(PackageVersions))]
		system := Systems[g.rng.Intn(len(Systems))]

		// Generate derivation path
		drvPath := GenerateDrvPath(g.rng, pkgName, version)

		// Determine build status (85% success rate by default)
		var buildStatus int32
		if g.rng.Intn(100) < g.config.SuccessRate {
			buildStatus = 0 // success
		} else {
			buildStatus = 1 // failed
		}

		// Build duration between 10 seconds and 30 minutes
		buildDuration := time.Duration(g.rng.Intn(30*60-10)+10) * time.Second
		startTime := timestamp
		stopTime := timestamp.Add(buildDuration)

		g.builds = append(g.builds, queries.CopyBuildsParams{
			JobsetID:    jobset.ID,
			Job:         pkgName + "-" + system,
			Drvpath:     drvPath,
			System:      system,
			Finished:    1,
			Timestamp:   int32(timestamp.Unix()), //nolint:gosec // test data within safe range
			Buildstatus: pgtype.Int4{Int32: buildStatus, Valid: true},
			Priority:    0,
			Starttime:   pgtype.Int4{Int32: int32(startTime.Unix()), Valid: true}, //nolint:gosec // test data
			Stoptime:    pgtype.Int4{Int32: int32(stopTime.Unix()), Valid: true},  //nolint:gosec // test data
		})

		// Generate build steps
		numSteps := g.randRange(g.config.MinStepsPerBuild, g.config.MaxStepsPerBuild)
		g.generateBuildSteps(buildID, pkgName, version, numSteps, buildStatus)
	}
}

// generateBuildSteps generates build step data and collects it in memory.
func (g *Generator) generateBuildSteps(
	buildID int32,
	pkgName, version string,
	numSteps int,
	buildStatus int32,
) {
	now := time.Now()

	for stepNr := range numSteps {
		// Generate a derivation path for this step
		stepName := fmt.Sprintf("%s-step%d", pkgName, stepNr)
		drvPath := GenerateDrvPath(g.rng, stepName, version)

		// Step status usually matches build, but might vary
		stepStatus := buildStatus
		if buildStatus == 0 && g.rng.Intn(100) < 5 {
			// 5% chance of a cached step
			stepStatus = 8
		}

		machine := MachineNames[g.rng.Intn(len(MachineNames))]
		stepDuration := time.Duration(g.rng.Intn(600)+10) * time.Second
		startTime := now.Add(-stepDuration)

		g.buildSteps = append(g.buildSteps, queries.CopyBuildStepsParams{
			Build:     buildID,
			Stepnr:    int32(stepNr), //nolint:gosec // step numbers are small
			Type:      0,             // 0 = build
			Drvpath:   pgtype.Text{String: drvPath, Valid: true},
			Status:    pgtype.Int4{Int32: stepStatus, Valid: true},
			Machine:   machine,
			Starttime: pgtype.Int4{Int32: int32(startTime.Unix()), Valid: true}, //nolint:gosec // test data
			Stoptime:  pgtype.Int4{Int32: int32(now.Unix()), Valid: true},       //nolint:gosec // test data
			Busy:      0,
		})

		// Generate outputs for this step
		numOutputs := g.randRange(g.config.MinOutputsPerStep, g.config.MaxOutputsPerStep)
		//nolint:gosec // step numbers are small
		g.generateStepOutputs(buildID, int32(stepNr), pkgName, version, numOutputs)
	}
}

// generateStepOutputs generates step output data and collects it in memory.
func (g *Generator) generateStepOutputs(
	buildID, stepNr int32,
	pkgName, version string,
	numOutputs int,
) {
	// Shuffle output names and pick the first numOutputs
	outputs := make([]string, len(OutputNames))
	copy(outputs, OutputNames)
	g.rng.Shuffle(len(outputs), func(i, j int) {
		outputs[i], outputs[j] = outputs[j], outputs[i]
	})

	for i := range numOutputs {
		if i >= len(outputs) {
			break
		}

		outputName := outputs[i]
		path := GenerateStorePath(g.rng, pkgName, version)

		// Add output suffix if not "out"
		if outputName != "out" {
			path += "-" + outputName
		}

		g.stepOutputs = append(g.stepOutputs, queries.CopyBuildStepOutputsParams{
			Build:  buildID,
			Stepnr: stepNr,
			Name:   outputName,
			Path:   pgtype.Text{String: path, Valid: true},
		})
	}
}

func (g *Generator) pickRandomJobsetNames(n int) []string {
	names := make([]string, len(JobsetNames))
	copy(names, JobsetNames)
	g.rng.Shuffle(len(names), func(i, j int) {
		names[i], names[j] = names[j], names[i]
	})

	if n > len(names) {
		n = len(names)
	}

	return names[:n]
}

func (g *Generator) randRange(minVal, maxVal int) int {
	if minVal >= maxVal {
		return minVal
	}

	return minVal + g.rng.Intn(maxVal-minVal+1)
}

// uploadToS3 uploads narinfo and NAR files for all step outputs using concurrent workers.
func (g *Generator) uploadToS3(ctx context.Context) {
	tb := g.tb
	numWorkers := 16

	tb.Logf("Phase 3: Uploading %d narinfo/nar file pairs to S3 with %d workers...", len(g.stepOutputs), numWorkers)

	s3Start := time.Now()

	// Create a channel to distribute work
	pathChan := make(chan string, 256)

	// Track progress atomically
	var uploaded atomic.Int64

	// Create an errgroup for executing the workers
	eg, egCtx := errgroup.WithContext(ctx)

	// Start the workers
	for range numWorkers {
		eg.Go(func() error {
			// Each worker needs its own RNG for deterministic but independent random data
			workerRng := rand.New(rand.NewSource(g.rng.Int63())) //nolint:gosec

			for storePath := range pathChan {
				if uploadErr := g.uploadToS3WithRNG(egCtx, storePath, workerRng); uploadErr != nil {
					return uploadErr
				}

				count := uploaded.Add(1)
				if count%10000 == 0 {
					tb.Logf("Uploaded %d/%d files...", count, len(g.stepOutputs))
				}
			}

			return nil
		})
	}

	// Send work to the workers
	for _, output := range g.stepOutputs {
		if output.Path.Valid {
			select {
			case pathChan <- output.Path.String:
			case <-egCtx.Done():
				break
			}
		}
	}

	// Close the channel to signal no more work
	close(pathChan)

	// Wait for all workers to complete
	if err := eg.Wait(); err != nil {
		tb.Fatalf("failed to upload narinfo/nar files: %v", err)
	}

	tb.Logf("S3 upload complete in %s: %d file pairs", time.Since(s3Start).Round(time.Millisecond), len(g.stepOutputs))
}

// uploadToS3WithRNG uploads a narinfo file and corresponding NAR file to S3.
// The narinfo is stored at <hash>.narinfo where hash is extracted from the store path.
// The NAR is stored at nar/<filehash>.nar where filehash is the SHA256 of the NAR content.
// This version takes a context and RNG for concurrent use.
func (g *Generator) uploadToS3WithRNG(ctx context.Context, storePath string, rng *rand.Rand) error {
	// Generate random NAR bytes (small, 64-256 bytes)
	narSize := 64 + rng.Intn(193) // 64-256 bytes
	narBytes := make([]byte, narSize)

	_, err := rng.Read(narBytes)
	if err != nil {
		return fmt.Errorf("failed to generate random NAR bytes: %w", err)
	}

	// Calculate SHA256 hash of NAR content
	narHash := sha256.Sum256(narBytes)
	fileHashStr := nixbase32.EncodeToString(narHash[:])

	// Extract store path hash for narinfo filename: /nix/store/<hash>-...
	// The hash starts at position 11 (len("/nix/store/")) and is 32 chars long
	storePathHash := storePath[11:43]
	narinfoKey := storePathHash + ".narinfo"
	narKey := "nar/" + fileHashStr + ".nar"

	// Generate narinfo content
	narinfoContent := fmt.Sprintf(`StorePath: %s
URL: %s
Compression: none
FileHash: sha256:%s
FileSize: %d
NarHash: sha256:%s
NarSize: %d
References:
Deriver: unknown-deriver
`, storePath, narKey, fileHashStr, narSize, fileHashStr, narSize)

	// Upload NAR file
	err = g.bucketClient.PutObject(ctx, narKey,
		bytes.NewReader(narBytes), int64(narSize),
		"application/x-nix-nar")
	if err != nil {
		return fmt.Errorf("failed to upload NAR %s: %w", narKey, err)
	}

	// Upload narinfo file
	err = g.bucketClient.PutObject(ctx, narinfoKey,
		bytes.NewReader([]byte(narinfoContent)), int64(len(narinfoContent)),
		"text/x-nix-narinfo")
	if err != nil {
		return fmt.Errorf("failed to upload narinfo %s: %w", narinfoKey, err)
	}

	return nil
}
