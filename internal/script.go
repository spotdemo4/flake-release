package flakerelease

import (
	"bytes"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const scriptDirVariable = "__flake_release_dir"

// scriptDirPrologue resolves the directory of the running script, following
// symlinks, so bundled paths can be referenced relative to it.
const scriptDirPrologue = scriptDirVariable + `=$(p=$0; while [ -L "$p" ]; do l=$(readlink "$p"); case $l in /*) p=$l ;; *) p=$(dirname -- "$p")/$l ;; esac; done; CDPATH= cd -P -- "$(dirname -- "$p")" && pwd)`

var (
	storeReference  = regexp.MustCompile(`/nix/store/[0-9abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+(?:/[A-Za-z0-9+._?=@%,~-]+)*`)
	storeRoot       = regexp.MustCompile(`^/nix/store/[0-9abcdfghijklmnpqrsvwxyz]{32}-[A-Za-z0-9+._?=-]+`)
	shellFlags      = regexp.MustCompile(`^[-+][abefhkmnptuvxBCEHPT]+$`)
	versionedPython = regexp.MustCompile(`^(python[23])\.[0-9]+$`)
)

type quoteContext int

const (
	unquoted quoteContext = iota
	singleQuoted
	doubleQuoted
	literalText
)

type scriptEdit struct {
	start int
	end   int
	text  string
	drop  bool
	local bool
}

type scriptReference struct {
	offset int
	value  string
}

func patchScripts(bundle string, output bundledOutput, outputs []bundledOutput) error {
	scripts, err := output.binFiles(isScriptPath)
	if err != nil {
		return err
	}

	// symlinks to scripts that are patched themselves are kept as symlinks
	patched := map[string]bool{}
	for _, script := range scripts {
		if info, err := os.Lstat(script.src); err == nil && info.Mode().IsRegular() {
			patched[script.src] = true
		}
	}

	resolve := scriptResolver(outputs)
	for _, script := range scripts {
		if info, err := os.Lstat(script.src); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if resolved, err := filepath.EvalSymlinks(script.src); err == nil && patched[resolved] {
				continue
			}
		}

		src, err := os.ReadFile(script.src)
		if err != nil {
			return err
		}
		if bytes.IndexByte(src, 0) >= 0 {
			continue
		}

		name, err := filepath.Rel(bundle, script.dst)
		if err != nil {
			return err
		}
		content, unresolved, err := rewriteScript(src, filepath.Dir(script.dst), resolve)
		if err != nil {
			itemWarn("could not parse script %s, only its interpreter was patched: %v", name, err)
		}
		if len(unresolved) > 0 {
			itemWarn("script %s still references the nix store: %s", name, strings.Join(unresolved, ", "))
		}
		if bytes.Equal(content, src) {
			continue
		}

		if err := output.materialize(script); err != nil {
			return err
		}
		if err := makeWritable(script.dst); err != nil {
			return err
		}
		if err := os.WriteFile(script.dst, content, 0); err != nil {
			return err
		}
	}
	return nil
}

func isScriptPath(path string) bool {
	if !executable(path) {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	header := make([]byte, 2)
	_, err = io.ReadFull(file, header)
	return err == nil && string(header) == "#!"
}

// scriptResolver maps a nix store path to its location in the bundle.
func scriptResolver(outputs []bundledOutput) func(string) (string, bool) {
	return func(reference string) (string, bool) {
		for _, output := range outputs {
			if target, ok := output.bundlePath(reference); ok {
				return target, true
			}
		}

		// outputs built with symlinkJoin mirror other store paths
		root := storeRoot.FindString(reference)
		rest := strings.TrimPrefix(reference, root)
		if root == "" || rest == "" {
			return "", false
		}
		resolved, err := filepath.EvalSymlinks(reference)
		if err != nil {
			return "", false
		}
		for _, output := range outputs {
			candidate := filepath.Join(output.Path, rest)
			if !pathWithin(candidate, output.Path) {
				continue
			}
			if mirrored, err := filepath.EvalSymlinks(candidate); err != nil || mirrored != resolved {
				continue
			}
			if target, ok := output.bundlePath(candidate); ok {
				return target, true
			}
		}
		return "", false
	}
}

func (output bundledOutput) bundlePath(path string) (string, bool) {
	if !pathWithin(path, output.Path) {
		return "", false
	}
	if output.flattenedSource != "" {
		switch path {
		case output.flattenedSource:
			return output.flattenedPath, true
		case output.Path, filepath.Dir(output.flattenedSource):
			return filepath.Dir(output.flattenedPath), true
		}
		return "", false
	}
	relative, err := filepath.Rel(output.Path, path)
	if err != nil {
		return "", false
	}
	return filepath.Join(output.root, relative), true
}

// rewriteScript replaces nix store references in a script. The interpreter is
// looked up through env, references resolved by resolve become relative to the
// script directory, programs from other store paths are looked up through
// PATH, and assignments of other store paths are removed. It returns the
// patched script and the store paths that are still referenced. If a shell
// script cannot be parsed, only its interpreter is patched and the parse error
// is returned alongside the result.
func rewriteScript(src []byte, scriptDir string, resolve func(string) (string, bool)) ([]byte, []string, error) {
	text := string(src)
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		lineEnd = len(text)
	}
	shebang, setFlags, interpreter := rewriteShebang(text[:lineEnd])
	header := scriptEdit{start: 0, end: lineEnd, text: shebang}
	if len(setFlags) > 0 {
		header.text += "\nset " + strings.Join(setFlags, " ")
	}

	if !isShellInterpreter(interpreter) {
		return []byte(header.text + text[lineEnd:]), storeRoots(scanReferences(header.text + text[lineEnd:])), nil
	}

	// bash reads "((" as arithmetic where POSIX sh reads nested subshells
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(bytes.NewReader(src), "")
	if err != nil {
		var posixErr error
		if file, posixErr = syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(bytes.NewReader(src), ""); posixErr == nil {
			err = nil
		}
	}
	if err != nil {
		unresolved := scanReferences(header.text + text[lineEnd:])
		return []byte(header.text + text[lineEnd:]), storeRoots(unresolved), err
	}

	var edits []scriptEdit
	var unresolved []scriptReference
	var stack []syntax.Node
	heredocs := map[*syntax.Word]bool{}
	visit := func(start int, end int, context quoteContext) {
		for _, match := range storeReference.FindAllStringIndex(text[start:end], -1) {
			reference := scriptReference{offset: start + match[0], value: text[start+match[0] : start+match[1]]}
			edit := scriptEdit{start: reference.offset, end: start + match[1]}

			if target, ok := resolve(reference.value); ok {
				relative, err := filepath.Rel(scriptDir, target)
				if err != nil || context == literalText {
					unresolved = append(unresolved, reference)
					continue
				}
				edit.text = quoteScriptPath(filepath.ToSlash(relative), context)
				edit.local = true
				edits = append(edits, edit)
				continue
			}
			if program, ok := storeProgram(reference.value); ok {
				edit.text = program
				edits = append(edits, edit)
				continue
			}
			if stmt := innermostStmt(stack); stmt != nil && isPureAssignment(stmt) {
				edits = append(edits, scriptEdit{
					start: int(stmt.Cmd.Pos().Offset()),
					end:   int(stmt.Cmd.End().Offset()),
					text:  ":",
					drop:  true,
				})
				continue
			}
			unresolved = append(unresolved, reference)
		}
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		switch node := node.(type) {
		case *syntax.Redirect:
			if node.Hdoc != nil {
				heredocs[node.Hdoc] = isQuotedWord(node.Word)
			}
		case *syntax.Lit:
			visit(int(node.ValuePos.Offset()), int(node.ValueEnd.Offset()), literalContext(stack, heredocs))
		case *syntax.SglQuoted:
			if !node.Dollar {
				visit(int(node.Left.Offset())+1, int(node.Right.Offset()), singleQuoted)
			}
		}
		return true
	})

	edits = pruneScriptEdits(edits)
	if slices.ContainsFunc(edits, func(edit scriptEdit) bool { return edit.local }) {
		header.text += "\n" + scriptDirPrologue
	}
	if header.text != text[:lineEnd] {
		edits = append(edits, header)
	}

	remaining := scanReferences(header.text)
	for _, reference := range unresolved {
		if !slices.ContainsFunc(edits, func(edit scriptEdit) bool {
			return edit.drop && edit.start <= reference.offset && reference.offset < edit.end
		}) {
			remaining = append(remaining, reference.value)
		}
	}
	return []byte(applyScriptEdits(text, edits)), storeRoots(remaining), nil
}

func rewriteShebang(line string) (string, []string, string) {
	if !strings.HasPrefix(line, "#!") {
		return line, nil, ""
	}
	fields := strings.Fields(line[2:])
	if len(fields) == 0 {
		return line, nil, ""
	}
	interpreter, args := fields[0], fields[1:]
	name := path.Base(interpreter)
	store := strings.HasPrefix(interpreter, "/nix/store/")

	if name == "env" {
		program := ""
		for _, arg := range args {
			if !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
				program = path.Base(arg)
				break
			}
		}
		if store {
			line = strings.TrimRight("#!/usr/bin/env "+strings.Join(args, " "), " ")
		}
		return line, nil, program
	}
	if !store {
		return line, nil, name
	}

	if match := versionedPython.FindStringSubmatch(name); match != nil {
		name = match[1]
	}
	if len(args) == 0 {
		return "#!/usr/bin/env " + name, nil, name
	}
	if isShellInterpreter(name) && !slices.ContainsFunc(args, func(arg string) bool { return !shellFlags.MatchString(arg) }) {
		return "#!/usr/bin/env " + name, args, name
	}
	return "#!/usr/bin/env -S " + name + " " + strings.Join(args, " "), nil, name
}

func isShellInterpreter(name string) bool {
	return name == "sh" || name == "bash" || name == "dash"
}

func literalContext(stack []syntax.Node, heredocs map[*syntax.Word]bool) quoteContext {
	for index := len(stack) - 2; index >= 0; index-- {
		switch node := stack[index].(type) {
		case *syntax.DblQuoted:
			return doubleQuoted
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return unquoted
		case *syntax.Word:
			if quoted, ok := heredocs[node]; ok {
				if quoted {
					return literalText
				}
				return doubleQuoted
			}
		}
	}
	return unquoted
}

func isQuotedWord(word *syntax.Word) bool {
	for _, part := range word.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok || strings.Contains(lit.Value, `\`) {
			return true
		}
	}
	return false
}

func innermostStmt(stack []syntax.Node) *syntax.Stmt {
	for _, node := range slices.Backward(stack) {
		if stmt, ok := node.(*syntax.Stmt); ok {
			return stmt
		}
	}
	return nil
}

func isPureAssignment(stmt *syntax.Stmt) bool {
	if stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) > 0 {
		return false
	}
	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		return len(cmd.Args) == 0 && len(cmd.Assigns) > 0
	case *syntax.DeclClause:
		switch cmd.Variant.Value {
		case "export", "declare", "local", "readonly", "typeset":
			return true
		}
	}
	return false
}

// storeProgram returns the name of a program referenced by its store path.
func storeProgram(reference string) (string, bool) {
	rest := strings.TrimPrefix(reference, storeRoot.FindString(reference))
	dir, name, ok := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if !ok || (dir != "bin" && dir != "sbin") || name == "" || name == "." || name == ".." || strings.Contains(name, "/") || strings.HasPrefix(name, ".") {
		return "", false
	}
	return name, true
}

func quoteScriptPath(relative string, context quoteContext) string {
	value := "${" + scriptDirVariable + "}"
	if relative != "." {
		value += "/" + relative
	}
	switch context {
	case singleQuoted:
		return `'"` + value + `"'`
	case doubleQuoted:
		return value
	default:
		return `"` + value + `"`
	}
}

// pruneScriptEdits removes duplicate edits and edits within removed statements.
func pruneScriptEdits(edits []scriptEdit) []scriptEdit {
	sort.SliceStable(edits, func(i int, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	var pruned []scriptEdit
	for _, edit := range edits {
		if len(pruned) > 0 {
			last := pruned[len(pruned)-1]
			if last.drop && edit.start < last.end {
				continue
			}
		}
		pruned = append(pruned, edit)
	}
	return pruned
}

func applyScriptEdits(text string, edits []scriptEdit) string {
	sort.SliceStable(edits, func(i int, j int) bool {
		return edits[i].start < edits[j].start
	})
	var builder strings.Builder
	last := 0
	for _, edit := range edits {
		if edit.start < last {
			continue
		}
		builder.WriteString(text[last:edit.start])
		builder.WriteString(edit.text)
		last = edit.end
	}
	builder.WriteString(text[last:])
	return builder.String()
}

func scanReferences(text string) []string {
	return storeReference.FindAllString(text, -1)
}

func storeRoots(references []string) []string {
	var roots []string
	for _, reference := range references {
		if root := storeRoot.FindString(reference); root != "" && !slices.Contains(roots, root) {
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots
}
