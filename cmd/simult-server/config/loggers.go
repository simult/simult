package config

import "github.com/simult/server/pkg/logger"

var (
	DebugLogger   logger.Logger = &logger.NullLogger{}
	InfoLogger    logger.Logger = &logger.NullLogger{}
	WarningLogger logger.Logger = &logger.NullLogger{}
	ErrorLogger   logger.Logger = &logger.NullLogger{}
)
