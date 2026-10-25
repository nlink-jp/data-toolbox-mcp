package cmd

import (
	"crypto/sha256"
	"encoding/hex"
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
}

// instructions returns the Dockerfile's instructions with comments dropped
// and backslash continuations joined, so a command cannot hide on a
// continuation line.
func instructions(dockerfile string) []string {
	var out []string
	var cur strings.Builder
	for _, line := range strings.Split(dockerfile, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || (trimmed == "" && cur.Len() == 0) {
			continue
		}
		cont := strings.HasSuffix(trimmed, `\`)
		cur.WriteString(strings.TrimSuffix(trimmed, `\`))
		cur.WriteString(" ")
		if !cont {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// packageInstall matches any way of installing a Python package: pip, pip3,
// python -m pip, uv, and the exec form ["pip", "install", ...].
var packageInstall = regexp.MustCompile(`\bpip3?\s+install\b|\bpython3?\s+-m\s+pip\s+install\b|\buv\s+(pip|add|sync|tool)\b|"pip3?"\s*,\s*"install"`)

// The lock is the only package source. pip may not accept an unhashed
// download, resolve a dependency the lock does not name, or build an sdist —
// building one fetches its build backend with no hash check.
func TestDockerfileInstallsOnlyFromHashedLock(t *testing.T) {
	var installs []string
	for _, ins := range instructions(embedded(t, "Dockerfile")) {
		if packageInstall.MatchString(ins) {
			installs = append(installs, ins)
		}
	}
	if len(installs) != 1 {
		t.Fatalf("the Dockerfile must install Python packages in exactly one place, found %d: %q", len(installs), installs)
	}
	for _, want := range []string{"--only-binary=:all:", "--require-hashes", "--no-deps", "-r /tmp/requirements.lock"} {
		if !strings.Contains(installs[0], want) {
			t.Errorf("the pip install is missing %q: %s", want, installs[0])
		}
	}
	if got := len(packageInstall.FindAllString(installs[0], -1)); got != 1 {
		t.Errorf("the install instruction runs %d installs, want 1: %s", got, installs[0])
	}
	found := false
	for _, ins := range instructions(embedded(t, "Dockerfile")) {
		if ins == "COPY requirements.lock /tmp/requirements.lock" {
			found = true
		}
	}
	if !found {
		t.Error("the Dockerfile must COPY requirements.lock to /tmp/requirements.lock")
	}
}

// lockInputHash is the last line `make runtime-lock` appends.
var lockInputHash = regexp.MustCompile(`(?m)^# requirements\.in sha256: ([0-9a-f]{64})$`)

// The lock must have been generated from the requirements.in that ships with
// it: editing the constraints without `make runtime-lock` would leave the
// image installing something the constraints no longer say.
func TestLockIsFromCurrentInput(t *testing.T) {
	m := lockInputHash.FindStringSubmatch(embedded(t, "requirements.lock"))
	if m == nil {
		t.Fatal("requirements.lock does not record its input's sha256 (run make runtime-lock)")
	}
	sum := sha256.Sum256([]byte(embedded(t, "requirements.in")))
	if got := hex.EncodeToString(sum[:]); got != m[1] {
		t.Errorf("requirements.lock was generated from another requirements.in (recorded %s, current %s): run make runtime-lock", m[1], got)
	}
}

// inRequirement matches one constraint line in requirements.in.
var inRequirement = regexp.MustCompile(`(?m)^([A-Za-z0-9][A-Za-z0-9_.-]*)\s*([~=<>!].*)$`)

// The manifest names exactly the packages requirements.in asks for.
func TestManifestNamesMatchInput(t *testing.T) {
	in := map[string]bool{}
	for _, m := range inRequirement.FindAllStringSubmatch(embedded(t, "requirements.in"), -1) {
		in[normalize(m[1])] = true
	}
	man := map[string]bool{}
	for _, p := range runtime.Default.Packages {
		man[normalize(p.Name)] = true
		if !in[normalize(p.Name)] {
			t.Errorf("manifest advertises %s, which requirements.in does not ask for", p.Name)
		}
	}
	for name := range in {
		if !man[name] {
			t.Errorf("requirements.in asks for %s, which the manifest does not advertise", name)
		}
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
		// A floor and nothing else: an upper bound would close the line again.
		if !regexp.MustCompile(`(?m)^` + name + `>=[0-9][0-9.]*\s*$`).MatchString(in) {
			t.Errorf("requirements.in must give %s a floor (>=) and no upper bound", name)
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
