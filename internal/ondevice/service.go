package ondevice

import (
	"fmt"
	"sort"
	"strings"

	"reManager/internal/component"
	"reManager/internal/executor"
	"reManager/internal/installer"
	"reManager/internal/vellum"
)

// Catalog is the part of vellum.MetadataStore the service reads, so tests can stand in for the network.
type Catalog interface {
	Refresh() error
	Ready() bool
	GetAllPackagesForDevice(deviceType, firmware, arch string) []vellum.Package
	ResolveVirtualChoices(requested, installed []string, deviceType, firmware, arch string) []vellum.VirtualChoice
}

// MaxPageSize keeps a page of packages well inside one AppLoad message.
const MaxPageSize = 25

// Service lists Vellum packages and installs them on the tablet it runs on.
type Service struct {
	catalog  Catalog
	metadata *vellum.MetadataStore
	vellum   *vellum.Client
	exec     executor.CommandExecutor
	device   Device
}

// metadata supplies the install hooks; in the real app it is the same store as catalog.
func NewService(catalog Catalog, metadata *vellum.MetadataStore, exec executor.CommandExecutor, device Device) *Service {
	return &Service{
		catalog:  catalog,
		metadata: metadata,
		vellum:   vellum.NewClient(exec),
		exec:     exec,
		device:   device,
	}
}

type PackageSummary struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	Description      string `json:"description"`
	Category         string `json:"category,omitempty"`
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	Compatible       bool   `json:"compatible"`
}

type PackageDetail struct {
	PackageSummary
	Author             string   `json:"author,omitempty"`
	License            string   `json:"license,omitempty"`
	URL                string   `json:"url,omitempty"`
	Depends            []string `json:"depends,omitempty"`
	Conflicts          []string `json:"conflicts,omitempty"`
	IncompatibleReason string   `json:"incompatibleReason,omitempty"`
}

type ListRequest struct {
	Query         string `json:"query"`
	InstalledOnly bool   `json:"installedOnly"`
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
}

type ListResult struct {
	Packages []PackageSummary `json:"packages"`
	Total    int              `json:"total"`
	Offset   int              `json:"offset"`
}

// Plan is what an install would do, worked out by Vellum without changing anything.
type Plan struct {
	Requested []string `json:"requested"`
	// Packages is everything that would be installed or changed, dependencies included.
	Packages []string `json:"packages"`
	// Upgrades maps a package that is already installed to "old -> new".
	Upgrades map[string]string `json:"upgrades,omitempty"`
	// Error is Vellum's explanation when the request cannot be satisfied; the plan is then not installable.
	Error string `json:"error,omitempty"`
}

type InstallResult struct {
	Success bool     `json:"success"`
	Errors  []string `json:"errors,omitempty"`
	// Deferred names follow-up steps a package asked for that this build does not run, such as rebuilding the hashtable.
	Deferred []string `json:"deferred,omitempty"`
}

type Progress struct {
	Package string `json:"package,omitempty"`
	Index   int    `json:"index"`
	Total   int    `json:"total"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Packages the catalogue lists but nobody browses for.
var hiddenPackages = map[string]bool{
	"vellum":                 true,
	"vellum-bash-completion": true,
	"mount-utils":            true,
	"/bin/sh":                true,
}

func hidden(name string) bool {
	return hiddenPackages[name] || vellum.IsVirtualPackage(name)
}

// Refresh fetches the catalogue; it needs the tablet to be online.
func (s *Service) Refresh() error {
	return s.catalog.Refresh()
}

func (s *Service) firmware() string {
	state, err := s.vellum.GetOSVersionState()
	if err != nil {
		return ""
	}
	return state.CurrentVersion
}

func (s *Service) catalogPackages() ([]vellum.Package, error) {
	if !s.catalog.Ready() {
		if err := s.catalog.Refresh(); err != nil {
			return nil, fmt.Errorf("could not load the package catalogue: %w", err)
		}
	}
	return s.catalog.GetAllPackagesForDevice(string(s.device.Type), s.firmware(), s.device.PackageArch()), nil
}

func summarize(pkg vellum.Package, installed map[string]string) PackageSummary {
	summary := PackageSummary{
		Name:        pkg.Name,
		Version:     pkg.Version,
		Description: pkg.Description,
		Compatible:  pkg.Compatible,
	}
	if len(pkg.Categories) > 0 {
		summary.Category = pkg.Categories[0]
	}
	if version, ok := installed[pkg.Name]; ok {
		summary.Installed = true
		summary.InstalledVersion = version
	}
	return summary
}

// List returns one page of the catalogue, sorted by name.
func (s *Service) List(req ListRequest) (*ListResult, error) {
	packages, err := s.catalogPackages()
	if err != nil {
		return nil, err
	}
	installed, err := s.vellum.ListInstalledWithVersions()
	if err != nil {
		return nil, fmt.Errorf("could not list installed packages: %w", err)
	}

	query := strings.ToLower(strings.TrimSpace(req.Query))
	matches := []PackageSummary{}
	for _, pkg := range packages {
		if hidden(pkg.Name) {
			continue
		}
		if _, ok := installed[pkg.Name]; req.InstalledOnly && !ok {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(pkg.Name), query) && !strings.Contains(strings.ToLower(pkg.Description), query) {
			continue
		}
		matches = append(matches, summarize(pkg, installed))
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Name < matches[j].Name })

	limit := req.Limit
	if limit <= 0 || limit > MaxPageSize {
		limit = MaxPageSize
	}
	offset := min(max(req.Offset, 0), len(matches))
	end := min(offset+limit, len(matches))
	return &ListResult{Packages: matches[offset:end], Total: len(matches), Offset: offset}, nil
}

func (s *Service) Detail(name string) (*PackageDetail, error) {
	packages, err := s.catalogPackages()
	if err != nil {
		return nil, err
	}
	installed, err := s.vellum.ListInstalledWithVersions()
	if err != nil {
		return nil, fmt.Errorf("could not list installed packages: %w", err)
	}
	for _, pkg := range packages {
		if pkg.Name != name {
			continue
		}
		var depends []string
		for _, dep := range pkg.Depends {
			if !hiddenPackages[vellum.ParseConstraint(dep).Name] {
				depends = append(depends, dep)
			}
		}
		return &PackageDetail{
			PackageSummary:     summarize(pkg, installed),
			Author:             pkg.UpstreamAuthor,
			License:            pkg.License,
			URL:                pkg.URL,
			Depends:            depends,
			Conflicts:          pkg.Conflicts,
			IncompatibleReason: pkg.IncompatibleReason,
		}, nil
	}
	return nil, fmt.Errorf("no package named %q in the catalogue", name)
}

func checkNames(names []string) error {
	if len(names) == 0 {
		return fmt.Errorf("no packages given")
	}
	for _, name := range names {
		if !vellum.IsValidPackageName(name) {
			return fmt.Errorf("%q is not a valid package name", name)
		}
	}
	return nil
}

// A package that needs "a launcher" or another virtual gets the catalogue's preferred provider added, so Vellum is not left to pick.
func (s *Service) withPreferredProviders(names []string) []string {
	if !s.catalog.Ready() {
		return names
	}
	installed, err := s.vellum.List()
	if err != nil {
		return names
	}
	result := names
	for _, choice := range s.catalog.ResolveVirtualChoices(names, installed, string(s.device.Type), s.firmware(), s.device.PackageArch()) {
		if provider := choice.PreferredProvider(); provider != "" {
			result = append([]string{provider}, result...)
		}
	}
	return result
}

// Plan asks Vellum what installing names would do. A request Vellum cannot satisfy comes back as a Plan with Error set, not as an error.
func (s *Service) Plan(names []string) (*Plan, error) {
	if err := checkNames(names); err != nil {
		return nil, err
	}
	toSimulate := s.withPreferredProviders(names)

	simulation, err := s.vellum.SimulateAddWithout(toSimulate, nil)
	// The catalogue comes from the internet but Vellum solves against its own cached index, so a package released since the last update reads as missing.
	if err != nil && vellum.IsMissingPackage(vellum.ResolutionFailure(err)) {
		if updateErr := s.vellum.UpdateIndex(); updateErr == nil {
			simulation, err = s.vellum.SimulateAddWithout(toSimulate, nil)
		}
	}
	if err != nil {
		failure := vellum.ResolutionFailure(err)
		if failure == "" {
			failure = err.Error()
		}
		return &Plan{Requested: names, Packages: []string{}, Error: failure}, nil
	}

	plan := &Plan{Requested: names, Packages: simulation.Packages}
	if plan.Packages == nil {
		plan.Packages = []string{}
	}
	if len(simulation.VersionChanges) > 0 {
		plan.Upgrades = make(map[string]string, len(simulation.VersionChanges))
		for _, change := range simulation.VersionChanges {
			plan.Upgrades[change.Name] = change.From + " -> " + change.To
		}
	}
	return plan, nil
}

// Install installs names and their dependencies, reporting progress as it goes. It blocks until Vellum is done.
func (s *Service) Install(names []string, onProgress func(Progress)) InstallResult {
	plan, err := s.Plan(names)
	if err != nil {
		return InstallResult{Errors: []string{err.Error()}}
	}
	if plan.Error != "" {
		return InstallResult{Errors: []string{plan.Error}}
	}
	if len(plan.Packages) == 0 {
		return InstallResult{Success: true}
	}

	// The same list Plan simulated, so what gets installed is what was shown.
	toInstall := s.withPreferredProviders(names)

	var deferred []string
	inst := installer.NewInstaller(s.vellum, s.metadata, s.exec)
	result := inst.Install(
		toInstall,
		plan.Packages,
		component.CommandContext{Arch: s.device.Arch, Device: s.device.Type},
		func(p executor.ProgressInfo) {
			if onProgress != nil {
				onProgress(Progress{
					Package: p.CurrentComponent,
					Index:   p.CurrentIndex,
					Total:   p.TotalComponents,
					Status:  string(p.Status),
					Message: p.Message,
				})
			}
		},
		// Post-install steps can restart the stock UI, which this build does not do yet, so each one is recorded and left for later.
		func(hook *component.HookExecutionResult) error {
			if hook.DialogConfig != nil && hook.DialogConfig.Title != "" {
				deferred = append(deferred, hook.DialogConfig.Title)
			} else if hook.Command != nil && hook.Command.Description != "" {
				deferred = append(deferred, hook.Command.Description)
			} else {
				deferred = append(deferred, "A post-install step")
			}
			return installer.ErrHookDeclined
		},
	)
	return InstallResult{Success: result.Success, Errors: result.Errors, Deferred: deferred}
}
