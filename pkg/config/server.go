package config

import (
	"github.com/spf13/pflag"
)

type Server struct {
	S3       S3       `mapstructure:"s3"`
	HTTP     HTTP     `mapstructure:"http"`
	Postgres Postgres `mapstructure:"postgres"`
}

func (c *Server) Validate() error {
	if err := c.S3.Validate(); err != nil {
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

// SetServerFlags configures the provided FlagSet with predefined flags. It modifies the passed FlagSet directly.
func SetServerFlags(fs *pflag.FlagSet) {
	fs.String("s3.endpoint", "", "S3 Endpoint URL")
	fs.String("s3.access_key", "", "S3 Access Key")
	fs.String("s3.secret_key", "", "S3 Secret Key")
	fs.String("s3.bucket_name", "", "S3 Bucket Name")
	fs.Bool("s3.ssl_enabled", false, "Use SSL when connecting to S3")

	fs.Int16("http.port", 7777, "HTTP port to listen on")
	fs.String("http.host", "127.0.0.1", "HTTP host to listen on")

	fs.String("postgres.url", "", "Postgres URL")
}
