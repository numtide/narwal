package config

type GC struct {
	S3       S3       `mapstructure:"s3"`
	Postgres Postgres `mapstructure:"postgres"`
}

func (c *GC) Validate() error {
	if err := c.S3.Validate(); err != nil {
		return err
	}

	if err := c.Postgres.Validate(); err != nil {
		return err
	}

	return nil
}
