// Package entrypoint contains the container entrypoint logic that previously
// lived in the standalone docker-run.sh script.
//
// When the binary is invoked as the container entrypoint (running as root with
// a target UID configured via the UID/GID environment variables) it:
//
//  1. generates config.yaml from the built-in example on first start,
//  2. generates registration.yaml once a config file is present (waiting and
//     retrying until the config is usable, instead of exiting and letting the
//     container restart-loop),
//  3. chowns the data files to the target UID/GID,
//  4. disables file logging if it points at a read-only location,
//  5. drops privileges to the target UID/GID and continues running the bridge.
//
// The data files live in the directory returned by dataDir: the conventional
// /data location used by the old docker-run.sh, so existing deployments keep
// working no matter which working directory the container runs with.
//
// When not in entrypoint mode (not root, or UID not set) the function is a
// no-op and the bridge starts directly.
package entrypoint

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

// envChild marks child processes spawned by this package so that they skip the
// entrypoint bootstrap and simply act on their own command-line arguments.
const envChild = "MATRIX_PYLON_ENTRYPOINT_CHILD"

// defaultDataDir is the conventional data directory, as used by the original
// docker-run.sh and the Dockerfile WORKDIR/VOLUME. It is a variable (rather
// than a constant) so that tests can override it.
var defaultDataDir = "/data"

// dataDirEnvVar optionally pins the data directory explicitly.
const dataDirEnvVar = "MATRIX_PYLON_DATA_DIR"

// retryDelay is how long the entrypoint waits between registration attempts
// while it is waiting for a usable config.
const retryDelay = 5 * time.Second

// readOnlyLogFile is the logging filename that gets stripped from the config
// because it points at a read-only location inside the image.
const readOnlyLogFile = "./logs/matrix-pylon.log"

// Handle runs the container entrypoint logic.
//
// It is a no-op unless the process is in entrypoint mode. When in entrypoint
// mode it either terminates the process (after a fatal setup error) or
// completes the setup (config/registration generation, permission fixing,
// privilege dropping) and returns so the caller can start the bridge.
func Handle() {
	if !isEntryPoint() {
		return
	}

	// One-shot flags (-e/-g/-h/-v, ...) are handled by the bridge's own flag
	// parsing; skip the container bootstrap so their documented semantics are
	// preserved.
	if hasSingleShotFlag(os.Args[1:]) {
		return
	}

	configPath := configFilePath()
	regPath := registrationFilePath()

	// Generate the config file on first run.
	if fileMissing(configPath) {
		fmt.Fprintln(os.Stderr, "Didn't find a config file.")
		if code := runSelf("-e", "-c", configPath); code != 0 {
			os.Exit(code)
		}
		fmt.Fprintln(os.Stderr, "Copied default config file to", configPath)
		fmt.Fprintln(os.Stderr, "Modify that config file to your liking (at minimum: homeserver.domain).")
		fmt.Fprintln(os.Stderr, "The registration file will be generated and the bridge will start once the config is valid.")
	}

	// Generate the registration file once a config is present. If the config
	// is not usable yet (e.g. homeserver.domain is still the example value)
	// the -g invocation fails; instead of exiting and letting the container
	// restart-loop, keep waiting and retry until it succeeds.
	if fileMissing(regPath) {
		fmt.Fprintln(os.Stderr, "Didn't find a registration file.")
		for {
			code := runSelf("-g", "-c", configPath, "-r", regPath)
			if code == 0 {
				fmt.Fprintln(os.Stderr, "Generated one for you.")
				fmt.Fprintln(os.Stderr, "See https://docs.mau.fi/bridges/general/registering-appservices.html on how to use it.")
				break
			}
			if code == 20 {
				fmt.Fprintln(os.Stderr, "Homeserver domain is not set; waiting for", configPath, "to be updated...")
			} else {
				fmt.Fprintln(os.Stderr, "Registration generation failed (exit code", code, "); will retry...")
			}
			time.Sleep(retryDelay)
		}
	}

	// mautrix resolves its config/registration from relative flag defaults
	// ("config.yaml" / "registration.yaml"). We cannot rewrite those defaults
	// from here (mauflag snapshots os.Args at package init), so instead we
	// change into the directory that holds our files. That makes the bridge's
	// relative lookups land on exactly the config and registration we set up,
	// regardless of the container's original working directory.
	if dir := filepath.Dir(configPath); dir != "" && dir != "." {
		if err := os.Chdir(dir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not change directory to %s: %v\n", dir, err)
		}
	}

	if err := fixPerms(configPath, regPath); err != nil {
		msg := "Warning: failed to fix data file permissions: " + err.Error()
		fmt.Fprintln(os.Stderr, msg)
	}

	if err := dropPrivileges(); err != nil {
		msg := "Failed to drop privileges: " + err.Error()
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
}

// dataDir returns the directory that holds config.yaml, registration.yaml and
// the logs.
//
// Resolution order:
//  1. the MATRIX_PYLON_DATA_DIR environment variable, if set;
//  2. the directory containing an existing config.yaml (preferring the
//     conventional /data location over the working directory), so existing
//     deployments keep working no matter which working directory the
//     container runs with;
//  3. /data if it exists (the container default);
//  4. the working directory.
func dataDir() string {
	if v := os.Getenv(dataDirEnvVar); v != "" {
		return v
	}

	wd, err := os.Getwd()
	if err != nil {
		wd = defaultDataDir
	}

	if !fileMissing(filepath.Join(defaultDataDir, "config.yaml")) {
		return defaultDataDir
	}
	if !fileMissing(filepath.Join(wd, "config.yaml")) {
		return wd
	}
	if isDir(defaultDataDir) {
		return defaultDataDir
	}
	return wd
}

// configFilePath returns the config file path: the -c/--config flag when
// given, otherwise the conventional name inside the data directory.
func configFilePath() string {
	if v, ok := flagValue(os.Args[1:], "-c", "--config"); ok {
		return v
	}
	return filepath.Join(dataDir(), "config.yaml")
}

// registrationFilePath returns the registration file path: the -r/--registration
// flag when given, otherwise the conventional name inside the data directory.
func registrationFilePath() string {
	if v, ok := flagValue(os.Args[1:], "-r", "--registration"); ok {
		return v
	}
	return filepath.Join(dataDir(), "registration.yaml")
}

// isEntryPoint reports whether the entrypoint bootstrap should run: the
// process is running as root, a target UID is configured, and this is not a
// child invocation.
func isEntryPoint() bool {
	if os.Getenv(envChild) != "" {
		return false
	}
	if os.Geteuid() != 0 {
		return false
	}
	return targetUID() > 0
}

// targetUID reads the desired UID from the environment, defaulting to 0.
func targetUID() int {
	v, _ := strconv.Atoi(os.Getenv("UID"))
	return v
}

// targetGID reads the desired GID from the environment, defaulting to the UID.
func targetGID() int {
	if g := atoiEnv("GID"); g != 0 {
		return g
	}
	return targetUID()
}

// atoiEnv parses an environment variable as an int, returning 0 when unset or
// invalid.
func atoiEnv(key string) int {
	v, _ := strconv.Atoi(os.Getenv(key))
	return v
}

// hasSingleShotFlag reports whether any argument is a flag that the bridge
// handles as a one-shot action (write example config, generate registration,
// show version/help).
func hasSingleShotFlag(args []string) bool {
	for _, a := range args {
		name := a
		if i := strings.IndexByte(a, '='); i >= 0 {
			name = a[:i]
		}
		switch name {
		case "-e", "-g", "-h", "-v",
			"--generate-example-config", "--generate-registration",
			"--help", "--version", "--version-json":
			return true
		}
	}
	return false
}

// flagValue finds the value of the first matching short or long flag.
func flagValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		for _, name := range names {
			if a == name && i+1 < len(args) {
				return args[i+1], true
			}
			if v, ok := strings.CutPrefix(a, name+"="); ok {
				return v, true
			}
		}
	}
	return "", false
}

// fileMissing reports whether the given path does not exist.
func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// runSelf re-invokes the current executable with the given arguments as a
// child process (marked so it skips the entrypoint bootstrap) and returns the
// child's exit code.
func runSelf(args ...string) int {
	self, err := os.Executable()
	if err != nil {
		msg := "Failed to locate executable: " + err.Error()
		fmt.Fprintln(os.Stderr, msg)
		return 1
	}

	cmd := exec.Command(self, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), envChild+"=1")

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		msg := "Failed to run " + self + ": " + err.Error()
		fmt.Fprintln(os.Stderr, msg)
		return 1
	}
	return 0
}

// fixPerms makes sure the data files are owned by the target UID/GID so the
// bridge can write to them. The parent directory of the config file is
// chowned recursively unless it is a system directory, in which case only the
// individual data files are fixed.
func fixPerms(configPath, regPath string) error {
	uid, gid := targetUID(), targetGID()

	cfgDir := filepath.Clean(filepath.Dir(configPath))
	if chownTreeUnsafe(cfgDir) {
		_ = os.Chown(configPath, uid, gid)
	} else {
		if err := chownTree(cfgDir, uid, gid); err != nil {
			return err
		}
	}
	if regPath != "" && filepath.Clean(filepath.Dir(regPath)) != cfgDir {
		_ = os.Chown(regPath, uid, gid)
	}

	removeReadOnlyLogWriter(configPath)
	return nil
}

// chownTreeUnsafe reports whether recursively chowning dir would be dangerous
// (i.e. it is a top-level or system directory).
func chownTreeUnsafe(dir string) bool {
	switch dir {
	case "/", "/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/var",
		"/home", "/opt", "/root", "/boot", "/srv", "/proc", "/sys", "/dev":
		return true
	}
	return strings.HasPrefix(dir, "/etc/") || strings.HasPrefix(dir, "/usr/")
}

// chownTree recursively chowns the tree rooted at dir to uid:gid, ignoring
// individual file errors.
func chownTree(dir string, uid, gid int) error {
	return filepath.Walk(dir, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		_ = os.Chown(path, uid, gid)
		return nil
	})
}

// removeReadOnlyLogWriter removes the file logging writer from the config when
// it references a read-only location.
func removeReadOnlyLogWriter(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return
	}

	logging := findMapValue(doc.Content[0], "logging")
	if logging == nil || logging.Kind != yaml.MappingNode {
		return
	}
	writers := findMapValue(logging, "writers")
	if writers == nil || writers.Kind != yaml.SequenceNode || len(writers.Content) < 2 {
		return
	}
	second := writers.Content[1]
	if second.Kind != yaml.MappingNode {
		return
	}
	if filename := findMapValue(second, "filename"); filename == nil || filename.Value != readOnlyLogFile {
		return
	}

	writers.Content = append(writers.Content[:1], writers.Content[2:]...)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return
	}
	_ = os.WriteFile(configPath, out, 0600)
}

// findMapValue returns the value node associated with key in a mapping node.
func findMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// dropPrivileges lowers the process credentials to the target GID and UID.
func dropPrivileges() error {
	if os.Getegid() != targetGID() {
		if err := syscall.Setgid(targetGID()); err != nil {
			return fmt.Errorf("setgid(%d): %w", targetGID(), err)
		}
	}
	if os.Geteuid() != targetUID() {
		if err := syscall.Setuid(targetUID()); err != nil {
			return fmt.Errorf("setuid(%d): %w", targetUID(), err)
		}
	}
	return nil
}
