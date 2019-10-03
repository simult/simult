package logger

type Logger interface {
	Print(...interface{})
	Printf(string, ...interface{})
	Println(...interface{})
}

type StdLogger interface {
	Print(...interface{})
	Printf(string, ...interface{})
	Println(...interface{})

	Fatal(...interface{})
	Fatalf(string, ...interface{})
	Fatalln(...interface{})

	Panic(...interface{})
	Panicf(string, ...interface{})
	Panicln(...interface{})
}

type NullLogger struct {
}

func (l *NullLogger) Print(...interface{}) {
	// it is a null logger
}

func (l *NullLogger) Printf(string, ...interface{}) {
	// it is a null logger
}

func (l *NullLogger) Println(...interface{}) {
	// it is a null logger
}
