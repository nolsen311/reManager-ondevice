package ondevice

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"reManager/internal/executor"
	"reManager/internal/vellum"
	"reManager/internal/version"
)

// Packages that Vellum can install from a repository other than its own, such as this app's, are not in the catalogue the lists are built from. They are found in Vellum's own index instead.

// ExtraSource marks a package that is not in Vellum's catalogue.
const ExtraSource = "another repository"

// "name-1.2.3-r0 aarch64 {origin} (licence)" as `vellum list --available` prints it.
var availableLine = regexp.MustCompile(`^(\S+)-(\d[^\s-]*-r\d+)\s+\S+\s+\{`)

// "name-1.2.3-r0 description:" heads each block `vellum info -d` prints.
var descriptionHead = regexp.MustCompile(`^(\S+)-(\d[^\s-]*-r\d+) description:\s*$`)

const extrasTTL = time.Minute

type extrasCache struct {
	mu   sync.Mutex
	at   time.Time
	pkgs []vellum.Package
}

// newestAvailable reads `vellum list --available`, which lists every version in every repository, and keeps the newest of each name.
func newestAvailable(output string) map[string]string {
	newest := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		m := availableLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if current, ok := newest[m[1]]; !ok || version.Compare(m[2], current) > 0 {
			newest[m[1]] = m[2]
		}
	}
	return newest
}

// descriptionsFor reads `vellum info -d`, which prints one block per version, and keeps each name's description at the wanted version.
func descriptionsFor(output string, wanted map[string]string) map[string]string {
	descriptions := map[string]string{}
	name, ver := "", ""
	var lines []string
	flush := func() {
		if name != "" && wanted[name] == ver {
			descriptions[name] = strings.TrimSpace(strings.Join(lines, " "))
		}
		name, ver, lines = "", "", nil
	}
	for _, line := range strings.Split(output, "\n") {
		if m := descriptionHead.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			flush()
			name, ver = m[1], m[2]
			continue
		}
		if name != "" && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "WARNING:") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	flush()
	return descriptions
}

// extraPackages returns the packages Vellum can install that the catalogue does not list. Vellum's solver decides whether one fits this tablet, so they are shown as installable and the plan says otherwise if not.
func (s *Service) extraPackages(catalogue []vellum.Package) []vellum.Package {
	s.extras.mu.Lock()
	defer s.extras.mu.Unlock()
	if s.extras.pkgs != nil && time.Since(s.extras.at) < extrasTTL {
		return s.extras.pkgs
	}

	// The device's list leaves out packages for other models, which are still in the catalogue and are not "another repository".
	listed := make(map[string]bool, len(catalogue))
	for _, pkg := range catalogue {
		listed[pkg.Name] = true
	}
	inCatalogue := func(name string) bool { return listed[name] || s.catalog.GetPackage(name) != nil }
	out, err := s.exec.ExecuteWithOutput(vellum.VellumBin + " list --available")
	if err != nil && out == "" {
		return nil
	}
	newest := newestAvailable(out)
	wanted := map[string]string{}
	var names []string
	for name, ver := range newest {
		if !inCatalogue(name) && !hidden(name) && !strings.HasSuffix(name, "-doc") && !strings.HasSuffix(name, "-dev") {
			wanted[name] = ver
			names = append(names, name)
		}
	}

	descriptions := map[string]string{}
	if len(names) > 0 {
		if out, err := s.exec.ExecuteWithOutput(vellum.VellumBin + " info -d " + executor.QuoteArgs(names)); err == nil || out != "" {
			descriptions = descriptionsFor(out, wanted)
		}
	}

	pkgs := make([]vellum.Package, 0, len(names))
	for _, name := range names {
		pkgs = append(pkgs, vellum.Package{
			Name:        name,
			Version:     wanted[name],
			Description: descriptions[name],
			Categories:  []string{ExtraSource},
			Compatible:  true,
		})
	}
	s.extras.pkgs = pkgs
	s.extras.at = time.Now()
	return pkgs
}
