package ondevice

import (
	"fmt"
	"slices"
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

// Upgrade is an installed package Vellum would move to another version.
type Upgrade struct {
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
}

type PackageDetail struct {
	PackageSummary
	// Protected packages are ones this app runs on, so it will not remove them.
	Protected          bool     `json:"protected"`
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

// The jobs the service can plan and run.
const (
	ActionInstall = "install"
	ActionRemove  = "remove"
	ActionUpgrade = "upgrade"
)

// Plan is what a job would do, worked out by Vellum without changing anything.
type Plan struct {
	Action string `json:"action"`
	// Requested is empty for an upgrade of everything.
	Requested []string `json:"requested"`
	// Packages is everything that would be installed, changed or removed, dependencies included.
	Packages []string `json:"packages"`
	// Keep lists installed packages a removal first marks as explicitly installed, so a need such as "a launcher" stays settled once the removed package no longer names them.
	Keep []string `json:"keep,omitempty"`
	// Upgrades maps a package that is already installed to "old -> new".
	Upgrades map[string]string `json:"upgrades,omitempty"`
	// Error says why the request cannot be carried out; the plan is then not runnable.
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

// What the app itself runs on. Removing one of these from inside the app would leave no app to finish or undo the job.
var protectedPackages = map[string]bool{
	"vellum":                true,
	"xovi":                  true,
	"qt-resource-rebuilder": true,
	"appload":               true,
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
			Protected:          protectedPackages[pkg.Name],
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
	// The worker starts with no catalogue loaded; without it the worker would install a different set than the plan the frontend showed.
	if !s.catalog.Ready() {
		s.catalog.Refresh()
	}
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
		return &Plan{Action: ActionInstall, Requested: names, Packages: []string{}, Error: failure}, nil
	}

	plan := &Plan{Action: ActionInstall, Requested: names, Packages: simulation.Packages}
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
	result := inst.Install(toInstall, plan.Packages, s.commandContext(), relay(onProgress), deferHooks(&deferred))
	return InstallResult{Success: result.Success, Errors: result.Errors, Deferred: deferred}
}

func (s *Service) commandContext() component.CommandContext {
	return component.CommandContext{Arch: s.device.Arch, Device: s.device.Type}
}

func relay(onProgress func(Progress)) executor.ProgressCallback {
	return func(p executor.ProgressInfo) {
		if onProgress != nil {
			onProgress(Progress{
				Package: p.CurrentComponent,
				Index:   p.CurrentIndex,
				Total:   p.TotalComponents,
				Status:  string(p.Status),
				Message: p.Message,
			})
		}
	}
}

// Steps a package asks for around an install or removal can restart the stock UI, which this build does not do yet, so each one is recorded and left for later.
func deferHooks(deferred *[]string) installer.HookCallback {
	return func(hook *component.HookExecutionResult) error {
		switch {
		case hook.DialogConfig != nil && hook.DialogConfig.Title != "":
			*deferred = append(*deferred, hook.DialogConfig.Title)
		case hook.Command != nil && hook.Command.Description != "":
			*deferred = append(*deferred, hook.Command.Description)
		default:
			*deferred = append(*deferred, "A follow-up step")
		}
		return installer.ErrHookDeclined
	}
}

func blockedPlan(action string, names []string, reason string) *Plan {
	return &Plan{Action: action, Requested: names, Packages: []string{}, Error: reason}
}

// PlanRemove asks Vellum what removing names would take with it. It refuses, with a reason in the plan, anything that would remove more than was asked for by surprise or take away what the app runs on.
func (s *Service) PlanRemove(names []string) (*Plan, error) {
	if err := checkNames(names); err != nil {
		return nil, err
	}
	installed, err := s.vellum.ListInstalledWithVersions()
	if err != nil {
		return nil, fmt.Errorf("could not list installed packages: %w", err)
	}
	for _, name := range names {
		if protectedPackages[name] {
			return blockedPlan(ActionRemove, names, name+" is part of what this app runs on. Remove it from a computer."), nil
		}
		if _, ok := installed[name]; !ok {
			return blockedPlan(ActionRemove, names, name+" is not installed."), nil
		}
	}

	simulation, err := s.vellum.SimulateDel(names...)
	var keep []string
	if err != nil {
		// Vellum will not choose between providers on its own: once the removed package no longer names, say, appload, a mod that only needs "a launcher" leaves the choice open even though appload is installed. Naming the installed provider settles it.
		var ok bool
		keep, ok = installedProviders(err.Error(), installed)
		if !ok {
			return blockedPlan(ActionRemove, names, err.Error()), nil
		}
		purged, purgeErr := s.vellum.SimulateAddPurges(keep, names)
		if purgeErr != nil {
			return blockedPlan(ActionRemove, names, purgeErr.Error()), nil
		}
		simulation = &vellum.SimulateDelResult{Packages: purged}
	}
	if len(simulation.Blocked) > 0 {
		var reasons []string
		for pkg, blockers := range simulation.Blocked {
			sort.Strings(blockers)
			reasons = append(reasons, pkg+" is needed by "+strings.Join(blockers, ", ")+".")
		}
		sort.Strings(reasons)
		return blockedPlan(ActionRemove, names, strings.Join(reasons, " ")+" Remove those first."), nil
	}
	removed := make(map[string]bool, len(simulation.Packages))
	for _, pkg := range simulation.Packages {
		removed[pkg] = true
		if protectedPackages[pkg] {
			return blockedPlan(ActionRemove, names, "This would also remove "+pkg+", which this app runs on. Do it from a computer."), nil
		}
	}
	for _, name := range names {
		if !removed[name] {
			return blockedPlan(ActionRemove, names, "Vellum would leave "+name+" installed, most likely because another mod needs it."), nil
		}
	}
	return &Plan{Action: ActionRemove, Requested: names, Packages: simulation.Packages, Keep: keep}, nil
}

// installedProviders reads a Vellum failure that left a virtual package unchosen and returns, for each such virtual, the one installed package that provides it. It reports false unless every virtual has exactly one installed provider.
func installedProviders(failure string, installed map[string]string) ([]string, bool) {
	virtuals := vellum.ParseUnselectedVirtuals(failure)
	if len(virtuals) == 0 {
		return nil, false
	}
	// The "provided by" list names each candidate as name-version, possibly several to a line, up to "required by".
	var keep []string
	current := ""
	var candidates []string
	finish := func() bool {
		if current == "" {
			return true
		}
		var found []string
		for _, candidate := range candidates {
			for name, version := range installed {
				if candidate == name+"-"+version && !slices.Contains(found, name) {
					found = append(found, name)
				}
			}
		}
		if len(found) != 1 {
			return false
		}
		if !slices.Contains(keep, found[0]) {
			keep = append(keep, found[0])
		}
		return true
	}
	inProviders := false
	for _, line := range strings.Split(failure, "\n") {
		trimmed := strings.TrimSpace(line)
		if name, ok := strings.CutSuffix(trimmed, " (virtual):"); ok && slices.Contains(virtuals, name) {
			if !finish() {
				return nil, false
			}
			current, candidates, inProviders = name, nil, false
			continue
		}
		if rest, ok := strings.CutPrefix(trimmed, "provided by:"); ok {
			inProviders = true
			trimmed = rest
		} else if strings.Contains(trimmed, ":") {
			inProviders = false
		}
		if inProviders && current != "" {
			candidates = append(candidates, strings.Fields(trimmed)...)
		}
	}
	if !finish() || len(keep) == 0 {
		return nil, false
	}
	return keep, true
}

// Remove removes names, and whatever was only installed for them, in one Vellum transaction.
func (s *Service) Remove(names []string, onProgress func(Progress)) InstallResult {
	plan, err := s.PlanRemove(names)
	if err != nil {
		return InstallResult{Errors: []string{err.Error()}}
	}
	if plan.Error != "" {
		return InstallResult{Errors: []string{plan.Error}}
	}
	if len(plan.Keep) > 0 {
		// Already installed, so this only records them as installed on purpose; no package changes.
		if err := s.vellum.AddStreaming(func(line string) {
			if onProgress != nil {
				onProgress(Progress{Status: string(executor.StatusInstalling), Message: line})
			}
		}, plan.Keep...); err != nil {
			return InstallResult{Errors: []string{"Could not mark " + strings.Join(plan.Keep, ", ") + " as installed, so nothing was removed: " + err.Error()}}
		}
	}
	var deferred []string
	inst := installer.NewInstaller(s.vellum, s.metadata, s.exec)
	result := inst.Uninstall(names, plan.Packages, false, true, s.commandContext(), relay(onProgress), deferHooks(&deferred))
	return InstallResult{Success: result.Success, Errors: result.Errors, Deferred: deferred}
}

func (s *Service) simulateUpgrade(names []string) ([]Upgrade, error) {
	candidates, err := s.vellum.SimulateUpgradeWithVersions(names...)
	if err != nil {
		return nil, err
	}
	// Only for the "from" column, so a failure costs that and not the answer.
	installed, _ := s.vellum.ListInstalledWithVersions()
	upgrades := []Upgrade{}
	for _, c := range candidates {
		if vellum.IsVirtualPackage(c.Name) {
			continue
		}
		upgrades = append(upgrades, Upgrade{Name: c.Name, From: installed[c.Name], To: c.Version})
	}
	sort.Slice(upgrades, func(i, j int) bool { return upgrades[i].Name < upgrades[j].Name })
	return upgrades, nil
}

// Upgrades refreshes Vellum's package index, which needs the tablet to be online, and returns what an upgrade of everything would change.
func (s *Service) Upgrades() ([]Upgrade, error) {
	// With a stale index the answer is still true of what the tablet knows about, so a failed refresh is not fatal.
	s.vellum.UpdateIndex()
	return s.simulateUpgrade(nil)
}

// PlanUpgrade asks Vellum what upgrading names would change; no names means everything.
func (s *Service) PlanUpgrade(names []string) (*Plan, error) {
	if len(names) > 0 {
		if err := checkNames(names); err != nil {
			return nil, err
		}
	} else {
		names = []string{}
	}
	upgrades, err := s.simulateUpgrade(names)
	if err != nil {
		return blockedPlan(ActionUpgrade, names, err.Error()), nil
	}
	plan := &Plan{Action: ActionUpgrade, Requested: names, Packages: []string{}}
	if len(upgrades) > 0 {
		plan.Upgrades = make(map[string]string, len(upgrades))
	}
	for _, u := range upgrades {
		plan.Packages = append(plan.Packages, u.Name)
		if u.From != "" {
			plan.Upgrades[u.Name] = u.From + " -> " + u.To
		} else {
			plan.Upgrades[u.Name] = "new, " + u.To
		}
	}
	return plan, nil
}

// Upgrade upgrades names, or everything when there are none.
func (s *Service) Upgrade(names []string, onProgress func(Progress)) InstallResult {
	plan, err := s.PlanUpgrade(names)
	if err != nil {
		return InstallResult{Errors: []string{err.Error()}}
	}
	if plan.Error != "" {
		return InstallResult{Errors: []string{plan.Error}}
	}
	if len(plan.Packages) == 0 {
		return InstallResult{Success: true}
	}

	var lastError string
	err = s.vellum.UpgradeStreaming(func(line string) {
		if strings.HasPrefix(strings.TrimSpace(line), "ERROR:") {
			lastError = strings.TrimSpace(line)
		}
		if onProgress == nil {
			return
		}
		p := Progress{Status: string(executor.StatusInstalling), Message: line}
		if pkg, index, total, ok := vellum.ParseUpgradeProgress(line); ok {
			p.Package, p.Index, p.Total = pkg, index, total
		}
		onProgress(p)
	}, names...)
	if err != nil {
		if lastError == "" {
			lastError = err.Error()
		}
		return InstallResult{Errors: []string{"Update failed: " + lastError}}
	}
	return InstallResult{Success: true}
}
