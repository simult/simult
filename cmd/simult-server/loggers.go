package main

import (
	"github.com/simult/server/pkg/config"
	"github.com/simult/server/pkg/httplb"
	"github.com/simult/server/pkg/logger"
)

var (
	errorLogger   logger.Logger = &logger.NullLogger{}
	warningLogger logger.Logger = &logger.NullLogger{}
	infoLogger    logger.Logger = &logger.NullLogger{}
	debugLogger   logger.Logger = &logger.NullLogger{}
)

func setLoggers(err, warn, info, dbg logger.Logger) {
	errorLogger = err
	warningLogger = warn
	infoLogger = info
	debugLogger = dbg

	config.SetLoggers(err, warn, info, dbg)
	httplb.SetLoggers(err, warn, info, dbg)
}
