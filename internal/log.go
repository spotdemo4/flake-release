package flakerelease

import (
	"fmt"
	"os"
)

func info(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func bold(value string) string {
	return value
}

func dim(value string) string {
	return value
}
