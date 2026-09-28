package flakerelease

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	testSelfStore      = "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-hello-1.0"
	testCoreutilsStore = "/nix/store/zyxwvsrqpnmlkjihgfdcba9876543210-coreutils-9.5"
	testJqStore        = "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-jq-1.7"
	testBashStore      = "/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-bash-5.3"
	testPythonStore    = "/nix/store/cccccccccccccccccccccccccccccccc-python3-3.12.8"
)

func testScriptResolver(reference string) (string, bool) {
	if reference != testSelfStore && !strings.HasPrefix(reference, testSelfStore+"/") {
		return "", false
	}
	return filepath.Join("/bundle", strings.TrimPrefix(reference, testSelfStore)), true
}

func TestRewriteScript(t *testing.T) {
	for _, test := range []struct {
		name       string
		src        string
		want       string
		unresolved []string
	}{
		{
			name: "makeWrapper",
			src: "#! " + testBashStore + "/bin/bash -e\n" +
				"PATH=${PATH:+':'$PATH':'}\n" +
				"PATH=${PATH/':''" + testCoreutilsStore + "/bin'':'/':'}\n" +
				"PATH='" + testCoreutilsStore + "/bin'$PATH\n" +
				"PATH=${PATH#':'}\n" +
				"PATH=${PATH%':'}\n" +
				"export PATH\n" +
				"exec -a \"$0\" \"" + testSelfStore + "/bin/.hello-wrapped\"  \"$@\"\n",
			want: "#!/usr/bin/env bash\n" +
				"set -e\n" +
				scriptDirPrologue + "\n" +
				"PATH=${PATH:+':'$PATH':'}\n" +
				":\n" +
				":\n" +
				"PATH=${PATH#':'}\n" +
				"PATH=${PATH%':'}\n" +
				"export PATH\n" +
				"exec -a \"$0\" \"${__flake_release_dir}/.hello-wrapped\"  \"$@\"\n",
		},
		{
			name: "store programs",
			src: "#!" + testBashStore + "/bin/sh\n" +
				"dir=$(" + testCoreutilsStore + "/bin/mktemp -d)\n" +
				testJqStore + "/bin/jq -r . \"$dir/file\" | '" + testCoreutilsStore + "/bin/sort'\n",
			want: "#!/usr/bin/env sh\n" +
				"dir=$(mktemp -d)\n" +
				"jq -r . \"$dir/file\" | 'sort'\n",
		},
		{
			name: "bundled PATH",
			src: "#!/bin/sh\n" +
				"PATH=\"" + testSelfStore + "/bin:$PATH\"\n" +
				"exec gzip \"$@\"\n",
			want: "#!/bin/sh\n" +
				scriptDirPrologue + "\n" +
				"PATH=\"${__flake_release_dir}:$PATH\"\n" +
				"exec gzip \"$@\"\n",
		},
		{
			name: "quoting",
			src: "#!/bin/sh\n" +
				"a='" + testSelfStore + "/share/x'\n" +
				"b=" + testSelfStore + "/share/x\n" +
				"c=\"${X:-" + testSelfStore + "/share/x}\"\n" +
				"d=${X:-" + testSelfStore + "/share/x}\n",
			want: "#!/bin/sh\n" +
				scriptDirPrologue + "\n" +
				"a=''\"${__flake_release_dir}/../share/x\"''\n" +
				"b=\"${__flake_release_dir}/../share/x\"\n" +
				"c=\"${X:-${__flake_release_dir}/../share/x}\"\n" +
				"d=${X:-\"${__flake_release_dir}/../share/x\"}\n",
		},
		{
			name: "heredocs",
			src: "#!/bin/sh\n" +
				"cat <<EOF\n" +
				testSelfStore + "/share/x\n" +
				"EOF\n" +
				"cat <<'EOF'\n" +
				testSelfStore + "/share/y\n" +
				"EOF\n",
			want: "#!/bin/sh\n" +
				scriptDirPrologue + "\n" +
				"cat <<EOF\n" +
				"${__flake_release_dir}/../share/x\n" +
				"EOF\n" +
				"cat <<'EOF'\n" +
				testSelfStore + "/share/y\n" +
				"EOF\n",
			unresolved: []string{testSelfStore},
		},
		{
			name: "nested assignment",
			src: "#!/bin/sh\n" +
				"if [ -z \"$X\" ]; then\n" +
				"  export X=" + testCoreutilsStore + "/share/x Y=" + testCoreutilsStore + "/share/y\n" +
				"fi\n",
			want: "#!/bin/sh\n" +
				"if [ -z \"$X\" ]; then\n" +
				"  :\n" +
				"fi\n",
		},
		{
			name: "unresolved",
			src: "#!/bin/sh\n" +
				"cat " + testCoreutilsStore + "/share/x\n",
			want: "#!/bin/sh\n" +
				"cat " + testCoreutilsStore + "/share/x\n",
			unresolved: []string{testCoreutilsStore},
		},
		{
			name: "posix subshells",
			src: "#!/bin/sh\n" +
				"PATH=\"" + testSelfStore + "/bin:$PATH\"\n" +
				"((echo a) | cat)\n",
			want: "#!/bin/sh\n" +
				scriptDirPrologue + "\n" +
				"PATH=\"${__flake_release_dir}:$PATH\"\n" +
				"((echo a) | cat)\n",
		},
		{
			name: "other interpreter",
			src: "#!" + testPythonStore + "/bin/python3.12\n" +
				"print(\"" + testCoreutilsStore + "/bin/ls\")\n",
			want: "#!/usr/bin/env python3\n" +
				"print(\"" + testCoreutilsStore + "/bin/ls\")\n",
			unresolved: []string{testCoreutilsStore},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, unresolved, err := rewriteScript([]byte(test.src), "/bundle/bin", testScriptResolver)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("rewriteScript() =\n%s\nwant\n%s", got, test.want)
			}
			if !slices.Equal(unresolved, test.unresolved) {
				t.Fatalf("rewriteScript() unresolved = %q; want %q", unresolved, test.unresolved)
			}
		})
	}
}

func TestRewriteScriptParseError(t *testing.T) {
	src := "#!" + testBashStore + "/bin/bash\n" +
		"echo \"" + testCoreutilsStore + "/bin/ls\n"
	got, unresolved, err := rewriteScript([]byte(src), "/bundle/bin", testScriptResolver)
	if err == nil {
		t.Fatal("rewriteScript returned nil error for an unterminated quote")
	}
	want := "#!/usr/bin/env bash\n" +
		"echo \"" + testCoreutilsStore + "/bin/ls\n"
	if string(got) != want {
		t.Fatalf("rewriteScript() =\n%s\nwant\n%s", got, want)
	}
	if !slices.Equal(unresolved, []string{testCoreutilsStore}) {
		t.Fatalf("rewriteScript() unresolved = %q; want coreutils", unresolved)
	}
}

func TestRewriteShebang(t *testing.T) {
	for _, test := range []struct {
		line        string
		want        string
		setFlags    []string
		interpreter string
	}{
		{line: "#!/nix/store/x-bash/bin/bash -e", want: "#!/usr/bin/env bash", setFlags: []string{"-e"}, interpreter: "bash"},
		{line: "#!/nix/store/x-python3/bin/python3.12", want: "#!/usr/bin/env python3", interpreter: "python3"},
		{line: "#!/nix/store/x-perl/bin/perl -w", want: "#!/usr/bin/env -S perl -w", interpreter: "perl"},
		{line: "#!/nix/store/x-coreutils/bin/env bash", want: "#!/usr/bin/env bash", interpreter: "bash"},
		{line: "#!/usr/bin/env -S python3 -u", want: "#!/usr/bin/env -S python3 -u", interpreter: "python3"},
		{line: "#!/bin/sh -e", want: "#!/bin/sh -e", interpreter: "sh"},
		{line: "echo hello", want: "echo hello"},
	} {
		got, setFlags, interpreter := rewriteShebang(test.line)
		if got != test.want || !slices.Equal(setFlags, test.setFlags) || interpreter != test.interpreter {
			t.Fatalf("rewriteShebang(%q) = %q, %q, %q; want %q, %q, %q", test.line, got, setFlags, interpreter, test.want, test.setFlags, test.interpreter)
		}
	}
}

func TestStoreProgram(t *testing.T) {
	for _, test := range []struct {
		reference string
		want      string
		ok        bool
	}{
		{reference: testCoreutilsStore + "/bin/ls", want: "ls", ok: true},
		{reference: testCoreutilsStore + "/sbin/ip", want: "ip", ok: true},
		{reference: testCoreutilsStore + "/bin", ok: false},
		{reference: testCoreutilsStore + "/bin/.ls-wrapped", ok: false},
		{reference: testCoreutilsStore + "/libexec/ls", ok: false},
		{reference: testCoreutilsStore + "/bin/sub/ls", ok: false},
	} {
		got, ok := storeProgram(test.reference)
		if got != test.want || ok != test.ok {
			t.Fatalf("storeProgram(%q) = %q, %v; want %q, %v", test.reference, got, ok, test.want, test.ok)
		}
	}
}

func TestBundlePath(t *testing.T) {
	flattened := bundledOutput{
		packageOutput:   packageOutput{Name: "out", Path: testSelfStore},
		root:            "/bundle",
		flattenedSource: testSelfStore + "/bin/hello",
		flattenedPath:   "/bundle/hello",
	}
	nested := bundledOutput{
		packageOutput: packageOutput{Name: "dev", Path: testSelfStore},
		root:          "/bundle/dev",
	}
	for _, test := range []struct {
		output bundledOutput
		path   string
		want   string
		ok     bool
	}{
		{output: flattened, path: testSelfStore + "/bin/hello", want: "/bundle/hello", ok: true},
		{output: flattened, path: testSelfStore + "/bin", want: "/bundle", ok: true},
		{output: flattened, path: testSelfStore, want: "/bundle", ok: true},
		{output: flattened, path: testSelfStore + "/share/hello", ok: false},
		{output: nested, path: testSelfStore + "/include/hello.h", want: "/bundle/dev/include/hello.h", ok: true},
		{output: nested, path: testCoreutilsStore + "/bin/ls", ok: false},
	} {
		got, ok := test.output.bundlePath(test.path)
		if got != test.want || ok != test.ok {
			t.Fatalf("bundlePath(%q) = %q, %v; want %q, %v", test.path, got, ok, test.want, test.ok)
		}
	}
}

func TestPreparePackageBundlePatchesScripts(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(filepath.Join(out, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "#!" + testBashStore + "/bin/bash\n" +
		"exec " + testCoreutilsStore + "/bin/cat \"$@\"\n"
	if err := os.WriteFile(filepath.Join(out, "bin", "app"), []byte(src), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("app", filepath.Join(out, "bin", "alias")); err != nil {
		t.Fatal(err)
	}

	bundle, err := preparePackageBundle([]packageOutput{{Name: "out", Path: out}}, "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	defer deletePath(bundle)

	got, err := os.ReadFile(filepath.Join(bundle, "bin", "app"))
	if err != nil {
		t.Fatal(err)
	}
	want := "#!/usr/bin/env bash\n" +
		"exec cat \"$@\"\n"
	if string(got) != want {
		t.Fatalf("bundled script =\n%s\nwant\n%s", got, want)
	}
	if !executable(filepath.Join(bundle, "bin", "app")) {
		t.Fatal("bundled script is not executable")
	}
	if target, err := os.Readlink(filepath.Join(bundle, "bin", "alias")); err != nil || target != "app" {
		t.Fatalf("bundled alias = %q, %v; want symlink to app", target, err)
	}

	original, err := os.ReadFile(filepath.Join(out, "bin", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != src {
		t.Fatal("source script was modified")
	}
}
