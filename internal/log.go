package flakerelease

import (
	"fmt"
	"strings"

	"github.com/fatih/color"
)

var (
	warnColor  = color.New(color.FgYellow)
	errorColor = color.New(color.FgRed)
	boldColor  = color.New(color.Bold)
	dimColor   = color.New(color.Faint)
)

func info(format string, args ...any) {
	fmt.Fprintf(color.Error, format+"\n", args...)
}

func warn(format string, args ...any) {
	writeBlock(warnColor, "", "warning: ", fmt.Sprintf(format, args...))
}

func section(format string, args ...any) {
	info("")
	boldColor.Fprintf(color.Error, format+"\n", args...)
}

func item(format string, args ...any) {
	boldColor.Fprintf(color.Error, "  "+format+"\n", args...)
}

func status(format string, args ...any) {
	info("    "+format, args...)
}

func detail(format string, args ...any) {
	dimColor.Fprintf(color.Error, "    "+format+"\n", args...)
}

func detailBlock(value string) {
	writeBlock(dimColor, "    ", "", value)
}

func itemWarn(format string, args ...any) {
	writeBlock(warnColor, "    ", "warning: ", fmt.Sprintf(format, args...))
}

func PrintError(err error) {
	if err == nil {
		return
	}
	writeBlock(errorColor, "", "error: ", err.Error())
}

func writeBlock(style *color.Color, indent string, prefix string, value string) {
	lines := strings.Split(value, "\n")
	continuation := strings.Repeat(" ", len(prefix))
	for index, line := range lines {
		linePrefix := prefix
		if index > 0 {
			linePrefix = continuation
		}
		style.Fprintf(color.Error, "%s%s%s\n", indent, linePrefix, line)
	}
}

func bold(value string) string {
	return boldColor.Sprint(value)
}

func dim(value string) string {
	return dimColor.Sprint(value)
}
