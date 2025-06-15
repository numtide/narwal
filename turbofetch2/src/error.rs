use std::fmt;
use std::io;

#[derive(Debug)]
pub enum TurbofetchError {
    Io(io::Error),
    Http(String),
    Aws(String),
    Config(String),
    Parse(String),
}

impl fmt::Display for TurbofetchError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            TurbofetchError::Io(e) => write!(f, "IO error: {}", e),
            TurbofetchError::Http(msg) => write!(f, "HTTP error: {}", msg),
            TurbofetchError::Aws(msg) => write!(f, "AWS error: {}", msg),
            TurbofetchError::Config(msg) => write!(f, "Configuration error: {}", msg),
            TurbofetchError::Parse(msg) => write!(f, "Parse error: {}", msg),
        }
    }
}

impl std::error::Error for TurbofetchError {}

impl From<io::Error> for TurbofetchError {
    fn from(error: io::Error) -> Self {
        TurbofetchError::Io(error)
    }
}

impl From<String> for TurbofetchError {
    fn from(error: String) -> Self {
        TurbofetchError::Config(error)
    }
}

pub type Result<T> = std::result::Result<T, TurbofetchError>;
