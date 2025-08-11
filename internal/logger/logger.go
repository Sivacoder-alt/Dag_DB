package logger

import (
    "os"
    "path/filepath"

    "github.com/sirupsen/logrus"
    "github.com/sivaram/dag-leveldb/internal/config"
)

func NewLogger(cfg *config.Config) (*logrus.Logger, error) {
    logger := logrus.New()

    // Set log level
    switch cfg.Logging.Level {
    case "debug":
        logger.SetLevel(logrus.DebugLevel)
    case "info":
        logger.SetLevel(logrus.InfoLevel)
    case "warn":
        logger.SetLevel(logrus.WarnLevel)
    case "error":
        logger.SetLevel(logrus.ErrorLevel)
    default:
        logger.SetLevel(logrus.InfoLevel)
    }

    // Set output
    if cfg.Logging.Output == "file" {
        logDir := filepath.Dir(cfg.Logging.File)
        if err := os.MkdirAll(logDir, 0755); err != nil {
            return nil, err
        }

        file, err := os.OpenFile(cfg.Logging.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
        if err != nil {
            return nil, err
        }
        logger.SetOutput(file)
    } else {
        logger.SetOutput(os.Stdout)
    }

    logger.SetFormatter(&logrus.JSONFormatter{})
    return logger, nil
}