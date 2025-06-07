
Plugged the tool in the flake

First run:

thread 'main' panicked at src/main.rs:59:14:
called `Result::unwrap()` on an `Err` value: DispatchFailure(DispatchFailure { source: ConnectorError { kind: Other(None), source: CredentialsNotLoaded(CredentialsNotLoaded { source: Some("no providers in chain provided credentials") }), connection: Unknown } })
note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace

It fails like that if you're missing the AWS credentials. Run `aws sso login`.
