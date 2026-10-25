package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nlink-jp/data-toolbox-mcp/internal/runtime"
	dtmruntime "github.com/nlink-jp/data-toolbox-mcp/runtime"
)

func embedded(t *testing.T, name string) string {
	t.Helper()
	data, err := dtmruntime.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(data)
}

// lockEntry matches one requirement in requirements.lock: `name==version \`.
var lockEntry = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9_.-]*)==([^ \\\n]+) \\$`)

// lockHash matches one hash continuation line.
var lockHash = regexp.MustCompile(`^\s+--hash=sha256:[0-9a-f]{64}( \\)?$`)

// normalize is PEP 503 name normalization: case and runs of -_. don't matter.
func normalize(name string) string {
	return strings.ToLower(regexp.MustCompile(`[-_.]+`).ReplaceAllString(name, "-"))
}

// parseLock returns name -> version, and fails the test on any requirement
// that is not an exact pin followed by at least one SHA256.
func parseLock(t *testing.T, lock string) map[string]string {
	t.Helper()
	pins := map[string]string{}
	lines := strings.Split(lock, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(line, " ") {
			if !lockHash.MatchString(line) {
				t.Errorf("lock line %d is neither a comment nor a sha256 hash: %q", i+1, line)
			}
			continue
		}
		m := lockEntry.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("lock line %d is not an exact `name==version` pin with hashes: %q", i+1, line)
			continue
		}
		if i+1 >= len(lines) || !lockHash.MatchString(lines[i+1]) {
			t.Errorf("%s==%s has no --hash=sha256 line", m[1], m[2])
		}
		pins[normalize(m[1])] = m[2]
	}
	return pins
}

// Every package the image installs, dependencies included, must be one exact
// version checked against its SHA256 — a version alone is a name a publisher
// can reuse with different content (ADR-0007, amended v0.8.4).
func TestLockPinsEveryPackageWithHashes(t *testing.T) {
	pins := parseLock(t, embedded(t, "requirements.lock"))
	if len(pins) == 0 {
		t.Fatal("requirements.lock pins nothing")
	}
}

// describe_runtime advertises the manifest; the lock is what is installed.
func TestManifestMatchesLock(t *testing.T) {
	pins := parseLock(t, embedded(t, "requirements.lock"))
	for _, p := range runtime.Default.Packages {
		want := "==" + pins[normalize(p.Name)]
		if pins[normalize(p.Name)] == "" {
			t.Errorf("manifest advertises %s, which requirements.lock does not install", p.Name)
			continue
		}
		if p.VersionConstraint != want {
			t.Errorf("%s: manifest advertises %q, requirements.lock installs %q", p.Name, p.VersionConstraint, want)
		}
	}
	in := embedded(t, "requirements.in")
	for _, p := range runtime.Default.Packages {
		if !regexp.MustCompile(`(?mi)^` + regexp.QuoteMeta(p.Name) + `[~=><]`).MatchString(in) {
			t.Errorf("%s is in the manifest but not in requirements.in", p.Name)
		}
	}
}

// The lock is the only package source, and pip may neither accept an
// unhashed download nor fetch a dependency the lock does not name.
func TestDockerfileInstallsOnlyFromHashedLock(t *testing.T) {
	s := embedded(t, "Dockerfile")
	// Judge the instruction lines only: the comments name these flags too.
	var installs []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "RUN ") && strings.Contains(line, "pip install") {
			installs = append(installs, line)
		}
	}
	if len(installs) != 1 {
		t.Fatalf("the Dockerfile must have exactly one RUN pip install, has %d", len(installs))
	}
	for _, want := range []string{"--require-hashes", "--no-deps", "-r /tmp/requirements.lock"} {
		if !strings.Contains(installs[0], want) {
			t.Errorf("the pip install is missing %q: %s", want, installs[0])
		}
	}
	if !strings.Contains(s, "\nCOPY requirements.lock /tmp/requirements.lock\n") {
		t.Error("the Dockerfile must COPY requirements.lock to /tmp/requirements.lock")
	}
}

// A tag moves to content nobody checked; the base is pinned by its multi-arch
// index digest (ADR-0005, amended v0.8.4).
func TestDockerfilePinsBaseByDigest(t *testing.T) {
	for _, line := range strings.Split(embedded(t, "Dockerfile"), "\n") {
		if !strings.HasPrefix(line, "FROM ") {
			continue
		}
		if !regexp.MustCompile(`^FROM python:3\.12-slim@sha256:[0-9a-f]{64}$`).MatchString(line) {
			t.Errorf("FROM must be python:3.12-slim pinned by digest: %q", line)
		}
	}
}

// pyarrow and Pillow ship fixes only in their newest line, so the lock's
// input keeps them on a floor; a ~= there would stop `make runtime-lock` from
// ever reaching a fix (ADR-0007, amended v0.8.3).
func TestLockInputKeepsFloors(t *testing.T) {
	in := embedded(t, "requirements.in")
	for _, name := range []string{"pyarrow", "Pillow"} {
		if !regexp.MustCompile(`(?m)^` + name + `>=`).MatchString(in) {
			t.Errorf("requirements.in must give %s a floor (>=)", name)
		}
	}
}

// The base and packages are fixed, so the only thing a rebuild takes on its
// own is Debian's signed security updates — which a cached build would skip,
// because the embedded Dockerfile never changes. A digest needs no pull
// policy, and --pull=newer would fetch a base nobody pinned.
func TestBuildArgsBypassCache(t *testing.T) {
	got := strings.Join(buildArgs("/tmp/ctx", []string{"img:v1", "img:latest"}), " ")
	want := "build --no-cache -t img:v1 -t img:latest /tmp/ctx"
	if got != want {
		t.Errorf("buildArgs = %q, want %q", got, want)
	}
}

// A rebuild must also take fixes to packages already in the base image, which
// installing fonts-noto-cjk alone never upgrades.
func TestDockerfileUpgradesBaseBeforeInstall(t *testing.T) {
	s := embedded(t, "Dockerfile")
	upgrade := strings.Index(s, "apt-get upgrade -y")
	install := strings.Index(s, "apt-get install -y --no-install-recommends")
	if upgrade < 0 {
		t.Fatal("the Dockerfile must run apt-get upgrade -y")
	}
	if install < 0 || upgrade > install {
		t.Error("apt-get upgrade must come before the apt-get install")
	}
	if !strings.Contains(s, "export DEBIAN_FRONTEND=noninteractive") {
		t.Error("DEBIAN_FRONTEND must cover every apt-get call in the RUN")
	}
}

// TestEmbeddedDockerfileContents pins the Dockerfile pieces that other
// components rely on. The Python packages are checked against the lock by
// TestManifestMatchesLock.
func TestEmbeddedDockerfileContents(t *testing.T) {
	s := embedded(t, "Dockerfile")
	want := []string{
		"FROM python:3.12-slim@sha256:", // ADR-0007: 3.12-slim; ADR-0005: by digest
		// OS-level requirements for CJK rendering (ADR-0007)
		"fonts-noto-cjk", "ca-certificates",
		// matplotlibrc font fallback (ADR-0007)
		"Noto Sans CJK JP", "MATPLOTLIBRC",
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Errorf("embedded Dockerfile missing %q", w)
		}
	}
}
