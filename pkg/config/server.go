package config

type Server struct {
	AWS      AWS      `mapstructure:"aws"`
	S3       S3       `mapstructure:"s3"`
	SQS      SQS      `mapstructure:"sqs"`
	HTTP     HTTP     `mapstructure:"http"`
	Postgres Postgres `mapstructure:"postgres"`
}

func (c *Server) Validate() error {
	if err := c.AWS.Validate(); err != nil {
		return err
	}

	if err := c.S3.Validate(); err != nil {
		return err
	}

	if err := c.SQS.Validate(); err != nil {
		return err
	}

	if err := c.HTTP.Validate(); err != nil {
		return err
	}

	if err := c.Postgres.Validate(); err != nil {
		return err
	}

	return nil
}
