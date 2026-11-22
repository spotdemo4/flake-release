package flakerelease

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseAPKSigningConfig(t *testing.T) {
	keystore := []byte("keystore\x00bytes")
	encoded := base64.StdEncoding.EncodeToString(keystore)

	signing, err := parseAPKSigningConfig(config{
		androidSigningKeystore:         encoded[:8] + "\n" + encoded[8:] + "\n",
		androidSigningKeystorePassword: "s3cret-store",
		androidSigningKeyAlias:         "release",
		androidSigningKeyPassword:      "s3cret-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(signing.keystore) != string(keystore) {
		t.Fatalf("keystore = %q; want %q", signing.keystore, keystore)
	}
	if signing.keystorePassword != "s3cret-store" || signing.keyAlias != "release" || signing.keyPassword != "s3cret-key" {
		t.Fatalf("signing config = %+v", signing)
	}

	unpadded, err := parseAPKSigningConfig(config{
		androidSigningKeystore:         strings.TrimRight(encoded, "="),
		androidSigningKeystorePassword: "s3cret-store",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(unpadded.keystore) != string(keystore) {
		t.Fatalf("unpadded keystore = %q; want %q", unpadded.keystore, keystore)
	}
}

func TestParseAPKSigningConfigDisabled(t *testing.T) {
	signing, err := parseAPKSigningConfig(config{})
	if err != nil {
		t.Fatal(err)
	}
	if signing != nil {
		t.Fatalf("signing config = %+v; want nil", signing)
	}
}

func TestParseAPKSigningConfigRejectsInvalidConfig(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("keystore"))
	for _, test := range []struct {
		name string
		cfg  config
		want string
	}{
		{
			name: "password without keystore",
			cfg:  config{androidSigningKeystorePassword: "s3cret-store"},
			want: "ANDROID_SIGNING_KEYSTORE_PASSWORD is set",
		},
		{
			name: "alias without keystore",
			cfg:  config{androidSigningKeyAlias: "release"},
			want: "ANDROID_SIGNING_KEY_ALIAS is set",
		},
		{
			name: "invalid base64",
			cfg:  config{androidSigningKeystore: "not base64!", androidSigningKeystorePassword: "s3cret-store"},
			want: "not a base64-encoded keystore",
		},
		{
			name: "missing password",
			cfg:  config{androidSigningKeystore: encoded},
			want: "ANDROID_SIGNING_KEYSTORE_PASSWORD is required",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAPKSigningConfig(test.cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseAPKSigningConfig() error = %v; want %q", err, test.want)
			}
		})
	}
}

// fakeAPKSigner replaces apksigner with a runner that writes a signed APK to --out and
// records each command.
func fakeAPKSigner(t *testing.T, signature testAPKSignature, verifyErr error) *[]commandOptions {
	t.Helper()
	var commands []commandOptions
	previous := apkCommands
	apkCommands = fakePackageCommandRunner{
		requireFunc: func(name string) error {
			if name != "apksigner" {
				t.Fatalf("required command %q; want apksigner", name)
			}
			return nil
		},
		runFunc: func(options commandOptions) error {
			commands = append(commands, options)
			switch options.args[0] {
			case "sign":
				keystore := options.args[slices.Index(options.args, "--ks")+1]
				data, err := os.ReadFile(keystore)
				if err != nil {
					t.Fatalf("reading keystore: %v", err)
				}
				if string(data) != "keystore" {
					t.Fatalf("keystore = %q; want keystore", data)
				}
				writeTestAPK(t, options.args[slices.Index(options.args, "--out")+1], signature, 0)
				return nil
			case "verify":
				return verifyErr
			}
			t.Fatalf("unexpected apksigner command %v", options.args)
			return nil
		},
	}
	t.Cleanup(func() { apkCommands = previous })
	return &commands
}

func TestPrepareAPKSignsUnsignedAPK(t *testing.T) {
	commands := fakeAPKSigner(t, testAPKSignedV2, nil)
	apk := filepath.Join(t.TempDir(), "app-release-unsigned.apk")
	writeTestAPK(t, apk, testAPKUnsigned, 0)

	out, cleanup, err := prepareAPK(&apkSigningConfig{
		keystore:         []byte("keystore"),
		keystorePassword: "s3cret-store",
		keyAlias:         "release",
		keyPassword:      "s3cret-key",
	}, apk)
	if err != nil {
		t.Fatal(err)
	}
	if out == apk {
		t.Fatal("prepareAPK() returned the unsigned APK")
	}
	if filepath.Base(out) != filepath.Base(apk) {
		t.Fatalf("signed APK name = %q; want %q", filepath.Base(out), filepath.Base(apk))
	}
	signed, err := apkSigned(out)
	if err != nil {
		t.Fatal(err)
	}
	if !signed {
		t.Fatal("prepareAPK() returned an unsigned APK")
	}

	if len(*commands) != 2 {
		t.Fatalf("ran %d apksigner commands; want 2", len(*commands))
	}
	sign := (*commands)[0]
	wantArgs := []string{
		"--ks-pass", "env:" + apkKeystorePasswordEnv,
		"--ks-key-alias", "release",
		"--key-pass", "env:" + apkKeyPasswordEnv,
		"--v4-signing-enabled", "false",
		"--out", out,
		"--in", apk,
	}
	joined := strings.Join(sign.args, " ")
	if !strings.Contains(joined, strings.Join(wantArgs, " ")) {
		t.Fatalf("apksigner sign args = %v; want them to contain %v", sign.args, wantArgs)
	}
	for _, secret := range []string{"s3cret-store", "s3cret-key"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("apksigner sign args contain password %q", secret)
		}
		if !slices.Contains(sign.secrets, secret) {
			t.Fatalf("apksigner sign secrets = %v; want them to contain %q", sign.secrets, secret)
		}
	}
	for _, want := range []string{apkKeystorePasswordEnv + "=s3cret-store", apkKeyPasswordEnv + "=s3cret-key"} {
		if !slices.Contains(sign.env, want) {
			t.Fatalf("apksigner sign env = %v; want it to contain %q", sign.env, want)
		}
	}
	if got := (*commands)[1].args; !slices.Equal(got, []string{"verify", out}) {
		t.Fatalf("apksigner verify args = %v", got)
	}

	keystore := sign.args[slices.Index(sign.args, "--ks")+1]
	if _, err := os.Stat(keystore); !os.IsNotExist(err) {
		t.Fatalf("keystore %s was not removed after signing: %v", keystore, err)
	}
	cleanup()
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("signed APK %s was not removed by cleanup: %v", out, err)
	}
}

func TestPrepareAPKOmitsUnsetKeyOptions(t *testing.T) {
	commands := fakeAPKSigner(t, testAPKSignedV2, nil)
	apk := filepath.Join(t.TempDir(), "app.apk")
	writeTestAPK(t, apk, testAPKUnsigned, 0)

	_, cleanup, err := prepareAPK(&apkSigningConfig{keystore: []byte("keystore"), keystorePassword: "s3cret-store"}, apk)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	sign := (*commands)[0]
	for _, flag := range []string{"--ks-key-alias", "--key-pass"} {
		if slices.Contains(sign.args, flag) {
			t.Fatalf("apksigner sign args = %v; want no %s", sign.args, flag)
		}
	}
	if slices.ContainsFunc(sign.env, func(value string) bool { return strings.HasPrefix(value, apkKeyPasswordEnv+"=") }) {
		t.Fatalf("apksigner sign env = %v; want no key password", sign.env)
	}
}

func TestPrepareAPKReleasesFilesAsIs(t *testing.T) {
	signing := &apkSigningConfig{keystore: []byte("keystore"), keystorePassword: "s3cret-store"}
	for _, test := range []struct {
		name      string
		filename  string
		signature testAPKSignature
		signing   *apkSigningConfig
	}{
		{name: "signed APK", filename: "app.apk", signature: testAPKSignedV2, signing: signing},
		{name: "v1 signed APK", filename: "app.apk", signature: testAPKSignedV1, signing: signing},
		{name: "unsigned APK without signing", filename: "app.apk", signature: testAPKUnsigned},
		{name: "app bundle", filename: "app.aab", signature: testAPKUnsigned, signing: signing},
		{name: "archive", filename: "app.zip", signature: testAPKUnsigned, signing: signing},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands := fakeAPKSigner(t, testAPKSignedV2, nil)
			path := filepath.Join(t.TempDir(), test.filename)
			writeTestAPK(t, path, test.signature, 0)

			out, cleanup, err := prepareAPK(test.signing, path)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if out != path {
				t.Fatalf("prepareAPK() = %q; want %q", out, path)
			}
			if len(*commands) != 0 {
				t.Fatalf("ran apksigner commands %v; want none", *commands)
			}
		})
	}
}

func TestPrepareAPKFailsWhenSigningFails(t *testing.T) {
	signing := &apkSigningConfig{keystore: []byte("keystore"), keystorePassword: "s3cret-store"}
	for _, test := range []struct {
		name      string
		signature testAPKSignature
		verifyErr error
		want      string
	}{
		{name: "verify fails", signature: testAPKSignedV2, verifyErr: errors.New("DOES NOT VERIFY"), want: "verifying signed APK"},
		{name: "output unsigned", signature: testAPKUnsigned, want: "did not sign"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fakeAPKSigner(t, test.signature, test.verifyErr)
			apk := filepath.Join(t.TempDir(), "app.apk")
			writeTestAPK(t, apk, testAPKUnsigned, 0)

			_, _, err := prepareAPK(signing, apk)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("prepareAPK() error = %v; want %q", err, test.want)
			}
		})
	}
}
