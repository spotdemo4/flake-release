package flakerelease

import (
	"fmt"

	"github.com/fatih/color"
)

var (
	warnColor = color.New(color.FgYellow)
	boldColor = color.New(color.Bold)
	dimColor  = color.New(color.Faint)
)

func info(format string, args ...any) {
	fmt.Fprintf(color.Error, format+"\n", args...)
}

func warn(format string, args ...any) {
	warnColor.Fprintf(color.Error, format+"\n", args...)
}

func bold(value string) string {
	return boldColor.Sprint(value)
}

func dim(value string) string {
	return dimColor.Sprint(value)
}
