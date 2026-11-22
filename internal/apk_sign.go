package flakerelease

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	apkKeystorePasswordEnv = "FLAKE_RELEASE_APK_KEYSTORE_PASSWORD"
	apkKeyPasswordEnv      = "FLAKE_RELEASE_APK_KEY_PASSWORD"
)

// apkSigningConfig is the keystore that signs APKs before they are released.
type apkSigningConfig struct {
	keystore         []byte
	keystorePassword string
	keyAlias         string
	keyPassword      string
}

// apkCommands runs apksigner, and is replaced in tests.
var apkCommands packageCommandRunner = execPackageCommandRunner{}

// parseAPKSigningConfig returns the APK signing keystore from the ANDROID_SIGNING_
// variables, or nil when ANDROID_SIGNING_KEYSTORE is unset.
func parseAPKSigningConfig(cfg config) (*apkSigningConfig, error) {
	encoded := strings.Join(strings.Fields(cfg.androidSigningKeystore), "")
	if encoded == "" {
		for name, value := range map[string]string{
			"ANDROID_SIGNING_KEYSTORE_PASSWORD": cfg.androidSigningKeystorePassword,
			"ANDROID_SIGNING_KEY_ALIAS":         cfg.androidSigningKeyAlias,
			"ANDROID_SIGNING_KEY_PASSWORD":      cfg.androidSigningKeyPassword,
		} {
			if value != "" {
				return nil, fmt.Errorf("%s is set, but ANDROID_SIGNING_KEYSTORE is not", name)
			}
		}
		return nil, nil
	}

	keystore, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		keystore, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil {
		return nil, errors.New("ANDROID_SIGNING_KEYSTORE is not a base64-encoded keystore")
	}
	if len(keystore) == 0 {
		return nil, errors.New("ANDROID_SIGNING_KEYSTORE is empty")
	}
	if cfg.androidSigningKeystorePassword == "" {
		return nil, errors.New("ANDROID_SIGNING_KEYSTORE_PASSWORD is required to sign APKs with ANDROID_SIGNING_KEYSTORE")
	}
	return &apkSigningConfig{
		keystore:         keystore,
		keystorePassword: cfg.androidSigningKeystorePassword,
		keyAlias:         cfg.androidSigningKeyAlias,
		keyPassword:      cfg.androidSigningKeyPassword,
	}, nil
}

// prepareAPK signs an unsigned APK when signing is configured, and returns the path to
// release along with a function that removes any signed copy. Other files, and APKs
// that are already signed, are released as they are.
func prepareAPK(signing *apkSigningConfig, path string) (string, func(), error) {
	noop := func() {}
	if !strings.EqualFold(filepath.Ext(path), ".apk") {
		return path, noop, nil
	}
	signed, err := apkSigned(path)
	if err != nil {
		return "", noop, err
	}
	switch {
	case signed && signing != nil:
		// re-signing would change the app's identity, so Android would refuse it as an update
		status("APK is already signed, releasing it as is")
		return path, noop, nil
	case signed:
		return path, noop, nil
	case signing == nil:
		itemWarn("APK is unsigned, so Android won't install it; set ANDROID_SIGNING_KEYSTORE to sign it")
		return path, noop, nil
	}

	status("signing APK")
	dir, err := os.MkdirTemp("", "flake-release-apk-*")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { deletePath(dir) }
	out, err := signAPK(signing, path, dir)
	if err != nil {
		cleanup()
		return "", noop, err
	}
	return out, cleanup, nil
}

// signAPK signs the APK at path with apksigner into dir, and verifies the result.
func signAPK(signing *apkSigningConfig, path string, dir string) (string, error) {
	if err := apkCommands.require("apksigner"); err != nil {
		return "", err
	}

	keystore := filepath.Join(dir, "keystore")
	if err := os.WriteFile(keystore, signing.keystore, 0o600); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(keystore) }()

	// passwords are passed through the environment so that they never appear in arguments
	env := []string{apkKeystorePasswordEnv + "=" + signing.keystorePassword}
	args := []string{
		"sign",
		"--ks", keystore,
		"--ks-pass", "env:" + apkKeystorePasswordEnv,
	}
	if signing.keyAlias != "" {
		args = append(args, "--ks-key-alias", signing.keyAlias)
	}
	if signing.keyPassword != "" {
		env = append(env, apkKeyPasswordEnv+"="+signing.keyPassword)
		args = append(args, "--key-pass", "env:"+apkKeyPasswordEnv)
	}
	out := filepath.Join(dir, filepath.Base(path))
	// v4 signatures are written to a separate .idsig file, which is only used by adb
	args = append(args, "--v4-signing-enabled", "false", "--out", out, "--in", path)
	secrets := []string{signing.keystorePassword, signing.keyPassword}
	if err := apkCommands.run(commandOptions{name: "apksigner", args: args, env: env, secrets: secrets}); err != nil {
		return "", fmt.Errorf("signing APK %s: %w", filepath.Base(path), err)
	}

	if err := apkCommands.run(commandOptions{name: "apksigner", args: []string{"verify", out}}); err != nil {
		return "", fmt.Errorf("verifying signed APK %s: %w", filepath.Base(path), err)
	}
	signed, err := apkSigned(out)
	if err != nil {
		return "", err
	}
	if !signed {
		return "", fmt.Errorf("apksigner did not sign APK %s", filepath.Base(path))
	}
	return out, nil
}
