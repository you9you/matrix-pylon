package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// overrideDataDir swaps defaultDataDir for the duration of a test and restores
// the original on cleanup.
func overrideDataDir(t *testing.T, v string) {
	t.Helper()
	old := defaultDataDir
	defaultDataDir = v
	t.Cleanup(func() { defaultDataDir = old })
}

func TestFlagValue(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		names  []string
		want   string
		wantOk bool
	}{
		{name: "short flag separate value", args: []string{"-c", "my.yaml"}, names: []string{"-c", "--config"}, want: "my.yaml", wantOk: true},
		{name: "long flag equals", args: []string{"--config=my.yaml"}, names: []string{"-c", "--config"}, want: "my.yaml", wantOk: true},
		{name: "long flag separate", args: []string{"--config", "my.yaml"}, names: []string{"-c", "--config"}, want: "my.yaml", wantOk: true},
		{name: "not present", args: []string{"--other", "x"}, names: []string{"-c", "--config"}, want: "", wantOk: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := flagValue(tc.args, tc.names...)
			if got != tc.want || ok != tc.wantOk {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOk)
			}
		})
	}
}

func TestHasSingleShotFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{name: "empty args", args: nil, want: false},
		{name: "normal run args", args: []string{"-c", "config.yaml"}, want: false},
		{name: "short e flag", args: []string{"-e"}, want: true},
		{name: "short g flag", args: []string{"-g", "-c", "x"}, want: true},
		{name: "long generate-registration", args: []string{"--generate-registration"}, want: true},
		{name: "help flag", args: []string{"--help"}, want: true},
		{name: "version json", args: []string{"--version-json"}, want: true},
		{name: "equals form", args: []string{"-e=true"}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSingleShotFlag(tc.args); got != tc.want {
				t.Fatalf("hasSingleShotFlag(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestDataDirEnvOverride(t *testing.T) {
	old := defaultDataDir
	defaultDataDir = t.TempDir() // force the default path to be a clean temp dir
	t.Cleanup(func() { defaultDataDir = old })

	t.Setenv(dataDirEnvVar, "/custom/dir")
	if got := dataDir(); got != "/custom/dir" {
		t.Fatalf("dataDir = %q, want %q", got, "/custom/dir")
	}
}

func TestDataDirPrefersDefaultOverCWD(t *testing.T) {
	// Simulate: conventional /data exists and has a config; CWD has one too.
	// The conventional location must win.
	t.Setenv(dataDirEnvVar, "")

	defaultDataDir = t.TempDir() // stand-in for /data
	t.Cleanup(func() { defaultDataDir = "/data" })

	if err := os.WriteFile(filepath.Join(defaultDataDir, "config.yaml"), []byte("a: 1"), 0600); err != nil {
		t.Fatal(err)
	}

	wd := t.TempDir()
	t.Chdir(wd)
	if err := os.WriteFile("config.yaml", []byte("b: 2"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := dataDir(); got != defaultDataDir {
		t.Fatalf("dataDir = %q, want %q (default wins over CWD)", got, defaultDataDir)
	}
}

func TestDataDirUsesCWDWhenDefaultEmpty(t *testing.T) {
	// No config in the conventional dir, but one exists in the CWD -> use CWD.
	t.Setenv(dataDirEnvVar, "")

	defaultDataDir = t.TempDir() // exists, but empty
	t.Cleanup(func() { defaultDataDir = "/data" })

	wd := t.TempDir()
	t.Chdir(wd)
	if err := os.WriteFile("config.yaml", []byte("c: 3"), 0600); err != nil {
		t.Fatal(err)
	}

	if got := dataDir(); got != wd {
		t.Fatalf("dataDir = %q, want %q (CWD with config)", got, wd)
	}
}

func TestDataDirDefaultWhenNoConfigAnywhere(t *testing.T) {
	// Nothing anywhere: conventional /data directory existing => use it.
	t.Setenv(dataDirEnvVar, "")

	defaultDataDir = t.TempDir() // exists, no config
	t.Cleanup(func() { defaultDataDir = "/data" })

	wd := t.TempDir()
	t.Chdir(wd)

	if got := dataDir(); got != defaultDataDir {
		t.Fatalf("dataDir = %q, want %q (default dir exists)", got, defaultDataDir)
	}
}

func TestDataDirFallsBackToCWDNoDefaultDir(t *testing.T) {
	// No default dir at all, no config anywhere: use CWD.
	t.Setenv(dataDirEnvVar, "")

	defaultDataDir = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { defaultDataDir = "/data" })

	wd := t.TempDir()
	t.Chdir(wd)

	if got := dataDir(); got != wd {
		t.Fatalf("dataDir = %q, want %q (CWD fallback)", got, wd)
	}
}

func TestConfigAndRegistrationFilePaths(t *testing.T) {
	t.Setenv(dataDirEnvVar, "/my/data")
	if got := configFilePath(); got != "/my/data/config.yaml" {
		t.Fatalf("configFilePath = %q, want %q", got, "/my/data/config.yaml")
	}
	if got := registrationFilePath(); got != "/my/data/registration.yaml" {
		t.Fatalf("registrationFilePath = %q, want %q", got, "/my/data/registration.yaml")
	}
}

func TestChownTreeUnsafe(t *testing.T) {
	unsafe := []string{"/", "/etc", "/usr", "/bin", "/var", "/home", "/opt", "/etc/nginx", "/usr/local"}
	for _, dir := range unsafe {
		if !chownTreeUnsafe(dir) {
			t.Fatalf("chownTreeUnsafe(%q) = false, want true", dir)
		}
	}
	safe := []string{"/data", "/config", "/var/lib/matrix-pylon", "/app/data"}
	for _, dir := range safe {
		if chownTreeUnsafe(dir) {
			t.Fatalf("chownTreeUnsafe(%q) = true, want false", dir)
		}
	}
}

func TestTargetUIDGID(t *testing.T) {
	t.Setenv("UID", "1337")
	t.Setenv("GID", "1338")
	if got := targetUID(); got != 1337 {
		t.Fatalf("targetUID = %d, want 1337", got)
	}
	if got := targetGID(); got != 1338 {
		t.Fatalf("targetGID = %d, want 1338", got)
	}

	t.Setenv("GID", "")
	if got := targetGID(); got != 1337 {
		t.Fatalf("targetGID with empty GID = %d, want 1337", got)
	}

	t.Setenv("UID", "")
	if got := targetUID(); got != 0 {
		t.Fatalf("targetUID with empty UID = %d, want 0", got)
	}
}

func TestIsEntryPoint(t *testing.T) {
	t.Setenv(envChild, "1")
	t.Setenv("UID", "1337")
	if isEntryPoint() {
		t.Fatal("isEntryPoint = true, want false when child marker set")
	}

	t.Setenv(envChild, "")
	if os.Geteuid() == 0 {
		t.Setenv("UID", "")
	}
	if got := isEntryPoint(); got {
		t.Fatalf("isEntryPoint = true, want false in test env (uid=%d)", os.Geteuid())
	}
}

func TestRemoveReadOnlyLogWriter(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")

	content := []byte(`
logging:
  min_level: debug
  writers:
    - type: stdout
      format: pretty-colored
    - type: file
      format: json
      filename: ./logs/matrix-pylon.log
      max_size: 100
`)
	if err := os.WriteFile(cfg, content, 0600); err != nil {
		t.Fatal(err)
	}

	removeReadOnlyLogWriter(cfg)

	data, _ := os.ReadFile(cfg)
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Content) == 0 {
		t.Fatal("empty yaml document")
	}
	logging := findMapValue(doc.Content[0], "logging")
	writers := findMapValue(logging, "writers")
	if writers == nil || len(writers.Content) != 1 {
		t.Fatalf("expected 1 writer after removal, got %d", len(writers.Content))
	}
}

func TestRemoveReadOnlyLogWriterKeepsWhenNoMatch(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")

	content := []byte(`
logging:
  min_level: debug
  writers:
    - type: stdout
      format: pretty-colored
    - type: file
      format: json
      filename: ./logs/bridge.log
`)
	if err := os.WriteFile(cfg, content, 0600); err != nil {
		t.Fatal(err)
	}

	removeReadOnlyLogWriter(cfg)

	data, _ := os.ReadFile(cfg)
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Content) == 0 {
		t.Fatal("empty yaml document")
	}
	logging := findMapValue(doc.Content[0], "logging")
	writers := findMapValue(logging, "writers")
	if writers == nil || len(writers.Content) != 2 {
		t.Fatalf("expected 2 writers (unchanged), got %d", len(writers.Content))
	}
}
