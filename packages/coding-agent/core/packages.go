package core

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type packageEntry struct {
	Record   PackageRecord
	Setting  PackageSetting
	Filtered bool
}

type packageCommand string

const (
	packageCommandInstall packageCommand = "install"
	packageCommandRemove  packageCommand = "remove"
	packageCommandUpdate  packageCommand = "update"
	packageCommandList    packageCommand = "list"
)

type packageUpdateTarget struct {
	Kind   string
	Source string
}

type packageCommandOptions struct {
	Command            packageCommand
	Source             string
	UpdateTarget       packageUpdateTarget
	Local              bool
	Force              bool
	Help               bool
	InvalidOption      string
	InvalidArgument    string
	MissingOptionValue string
	ConflictingOptions string
}

// PackageManager performs the actual install/remove/update side effects for
// the CLI package commands. The complete implementation — dependency install,
// staged rollback, npm uninstall, on-disk removal — lives in the codingagent
// DefaultPackageManager; core receives it through MainOptions to avoid an
// import cycle. When no manager is supplied, the legacy in-package installer is
// used as a fallback.
type PackageManager interface {
	InstallAndPersist(source string, local bool) error
	RemoveAndPersist(source string, local bool) (bool, error)
	Update(source string) error
}

// PackageManagerFactory builds a PackageManager bound to the given settings so
// install/remove/update persist into the same configuration core loaded.
type PackageManagerFactory func(cwd, agentDir string, settings *SettingsManager) PackageManager

func HandlePackageCommand(args []string, cwd, agentDir string, settings *SettingsManager, newManager PackageManagerFactory) (bool, error) {
	options, ok := parsePackageCommand(args)
	if !ok {
		return false, nil
	}
	if options.Help {
		printPackageCommandHelp(options.Command)
		return true, nil
	}
	if err := packageCommandValidationError(options); err != nil {
		return true, err
	}

	switch options.Command {
	case packageCommandInstall:
		if newManager != nil {
			return true, newManager(cwd, agentDir, settings).InstallAndPersist(options.Source, options.Local)
		}
		return true, installPackage(options.Source, options.Local, cwd, agentDir, settings)
	case packageCommandRemove:
		if newManager != nil {
			removed, err := newManager(cwd, agentDir, settings).RemoveAndPersist(options.Source, options.Local)
			if err != nil {
				return true, err
			}
			if removed {
				fmt.Println("Removed", options.Source)
			} else {
				fmt.Println("Package not found:", options.Source)
			}
			return true, nil
		}
		return true, removePackage(options.Source, options.Local, settings)
	case packageCommandList:
		listPackages(settings, cwd, agentDir)
		return true, nil
	case packageCommandUpdate:
		return true, updatePackages(options.UpdateTarget, settings, newManager, cwd, agentDir)
	}
	return false, nil
}

func parsePackageCommand(args []string) (packageCommandOptions, bool) {
	if len(args) == 0 {
		return packageCommandOptions{}, false
	}
	rawCommand := args[0]
	// "config" is intentionally not handled here: like the TypeScript
	// handlePackageCommand, the package CLI does not own config. The real
	// config command is handled by the top-level codingagent.handleConfigCommand
	// before core ever runs, so keeping a stub here only risks behaviour drift.
	command := packageCommand(rawCommand)
	if rawCommand == "uninstall" {
		command = packageCommandRemove
	}
	switch command {
	case packageCommandInstall, packageCommandRemove, packageCommandUpdate, packageCommandList:
	default:
		return packageCommandOptions{}, false
	}

	options := packageCommandOptions{Command: command}
	var selfFlag, extensionsFlag bool
	var extensionFlagSource string
	for index := 1; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help":
			options.Help = true
		case "-l", "--local":
			if command == packageCommandInstall || command == packageCommandRemove {
				options.Local = true
			} else if options.InvalidOption == "" {
				options.InvalidOption = arg
			}
		case "--self":
			if command == packageCommandUpdate {
				selfFlag = true
			} else if options.InvalidOption == "" {
				options.InvalidOption = arg
			}
		case "--extensions":
			if command == packageCommandUpdate {
				extensionsFlag = true
			} else if options.InvalidOption == "" {
				options.InvalidOption = arg
			}
		case "--force":
			if command == packageCommandUpdate {
				options.Force = true
			} else if options.InvalidOption == "" {
				options.InvalidOption = arg
			}
		case "--extension":
			if command != packageCommandUpdate {
				if options.InvalidOption == "" {
					options.InvalidOption = arg
				}
				continue
			}
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				if options.MissingOptionValue == "" {
					options.MissingOptionValue = arg
				}
				continue
			}
			if extensionFlagSource != "" {
				if options.ConflictingOptions == "" {
					options.ConflictingOptions = "--extension can only be provided once"
				}
				index++
				continue
			}
			index++
			extensionFlagSource = args[index]
		default:
			if strings.HasPrefix(arg, "-") {
				if options.InvalidOption == "" {
					options.InvalidOption = arg
				}
			} else if options.Source == "" {
				options.Source = arg
			} else if options.InvalidArgument == "" {
				options.InvalidArgument = arg
			}
		}
	}

	if command == packageCommandUpdate {
		options.UpdateTarget = packageUpdateTarget{Kind: "all"}
		switch {
		case extensionFlagSource != "":
			if selfFlag || extensionsFlag {
				options.ConflictingOptions = firstNonEmpty(options.ConflictingOptions, "--extension cannot be combined with --self or --extensions")
			}
			if options.Source != "" {
				options.ConflictingOptions = firstNonEmpty(options.ConflictingOptions, "--extension cannot be combined with a positional source")
			}
			options.UpdateTarget = packageUpdateTarget{Kind: "extensions", Source: extensionFlagSource}
		case options.Source != "":
			if options.Source == "self" || options.Source == "pi" {
				if extensionsFlag {
					options.UpdateTarget = packageUpdateTarget{Kind: "all"}
				} else {
					options.UpdateTarget = packageUpdateTarget{Kind: "self"}
				}
			} else {
				if extensionsFlag || selfFlag {
					options.ConflictingOptions = firstNonEmpty(options.ConflictingOptions, "positional update targets cannot be combined with --self or --extensions")
				}
				options.UpdateTarget = packageUpdateTarget{Kind: "extensions", Source: options.Source}
			}
		case selfFlag && extensionsFlag:
			options.UpdateTarget = packageUpdateTarget{Kind: "all"}
		case selfFlag:
			options.UpdateTarget = packageUpdateTarget{Kind: "self"}
		case extensionsFlag:
			options.UpdateTarget = packageUpdateTarget{Kind: "extensions"}
		}
	}
	return options, true
}

func packageCommandValidationError(options packageCommandOptions) error {
	switch {
	case options.InvalidOption != "":
		return fmt.Errorf("unknown option %s for %q", options.InvalidOption, options.Command)
	case options.MissingOptionValue != "":
		return fmt.Errorf("missing value for %s", options.MissingOptionValue)
	case options.InvalidArgument != "":
		return fmt.Errorf("unexpected argument %s", options.InvalidArgument)
	case options.ConflictingOptions != "":
		return fmt.Errorf("%s", options.ConflictingOptions)
	case (options.Command == packageCommandInstall || options.Command == packageCommandRemove) && options.Source == "":
		return fmt.Errorf("missing %s source", options.Command)
	default:
		return nil
	}
}

func printPackageCommandHelp(command packageCommand) {
	switch command {
	case packageCommandInstall:
		fmt.Println("Usage: pi install <source> [-l]")
	case packageCommandRemove:
		fmt.Println("Usage: pi remove <source> [-l]")
	case packageCommandUpdate:
		fmt.Println("Usage: pi update [source|self|pi] [--self] [--extensions] [--extension <source>] [--force]")
	case packageCommandList:
		fmt.Println("Usage: pi list")
	}
}

func installPackage(source string, local bool, cwd, agentDir string, settings *SettingsManager) error {
	root := filepath.Join(agentDir, "packages")
	if local {
		root = filepath.Join(ProjectPiDir(cwd), "packages")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	name := sanitizeSource(source)
	dest := filepath.Join(root, name)
	if strings.HasPrefix(source, "git:") || strings.HasPrefix(source, "https://") || strings.HasPrefix(source, "ssh://") {
		url := strings.TrimPrefix(source, "git:")
		if err := runPackageCommand("git", "clone", "--depth", "1", url, dest); err != nil {
			return err
		}
	} else if strings.HasPrefix(source, "npm:") {
		pkg := strings.TrimPrefix(source, "npm:")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		if err := runPackageCommandIn(dest, "npm", "pack", pkg); err != nil {
			return err
		}
		matches, _ := filepath.Glob(filepath.Join(dest, "*.tgz"))
		if len(matches) > 0 {
			if err := runPackageCommandIn(dest, "tar", "-xzf", matches[0], "--strip-components=1"); err != nil {
				return err
			}
			_ = os.Remove(matches[0])
		}
	} else {
		path := ResolveInCWD(cwd, source)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("unsupported package source or missing directory: %s", source)
		}
	}
	settings.AddPackageSource(normalizePackageSourceForSettings(source, cwd, agentDir, local), local)
	if local {
		return settings.SaveProject()
	}
	return settings.SaveGlobal()
}

// packageScopeBaseDir returns the directory that local package source paths are
// stored relative to, matching getBaseDirForScope() in package-manager.ts:
// project scope -> <cwd>/.pi, user scope -> agentDir.
func packageScopeBaseDir(cwd, agentDir string, local bool) string {
	if local {
		return ProjectPiDir(cwd)
	}
	return agentDir
}

// normalizePackageSourceForSettings rewrites a local package source so it is
// stored relative to the scope base directory. CLI input is resolved from cwd to
// locate the source; the stored value is then made relative to the scope base so
// list/update from a different cwd resolve back to the same directory. Managed
// sources (npm:, git:, URLs, github.com/...) are stored verbatim. Mirrors
// normalizePackageSourceForSettings() in package-manager.ts.
func normalizePackageSourceForSettings(source, cwd, agentDir string, local bool) string {
	trimmed := strings.TrimSpace(source)
	if isManagedPackageSource(trimmed) {
		return source
	}
	resolved := ResolveInCWD(cwd, trimmed)
	rel, err := filepath.Rel(packageScopeBaseDir(cwd, agentDir, local), resolved)
	if err != nil {
		return source
	}
	if rel == "" {
		return "."
	}
	return rel
}

func removePackage(source string, local bool, settings *SettingsManager) error {
	removed := removePackageSourceMatching(settings, source, local)
	removed = removeInstalledPackageMatching(settings, source, local) || removed
	if local {
		_ = settings.SaveProject()
	} else {
		_ = settings.SaveGlobal()
	}
	if removed {
		fmt.Println("Removed", source)
	} else {
		fmt.Println("Package not found:", source)
	}
	return nil
}

func listPackages(settings *SettingsManager, cwd, agentDir string) {
	user := packageEntries(settings, cwd, agentDir, false)
	project := packageEntries(settings, cwd, agentDir, true)
	if len(user) == 0 && len(project) == 0 {
		fmt.Println("No packages installed.")
		return
	}
	if len(user) > 0 {
		fmt.Println("User packages:")
		for _, entry := range user {
			printPackageEntry(entry)
		}
	}
	if len(project) > 0 {
		if len(user) > 0 {
			fmt.Println()
		}
		fmt.Println("Project packages:")
		for _, entry := range project {
			printPackageEntry(entry)
		}
	}
}

func printPackageEntry(entry packageEntry) {
	filter := ""
	if entry.Filtered {
		filter = " (filtered)"
	}
	fmt.Printf("  %s%s\n", entry.Record.Source, filter)
	if entry.Record.Path != "" {
		fmt.Printf("    %s\n", entry.Record.Path)
	}
}

func updatePackages(target packageUpdateTarget, settings *SettingsManager, newManager PackageManagerFactory, cwd, agentDir string) error {
	if target.Kind == "all" || target.Kind == "extensions" {
		if newManager != nil {
			// The DefaultPackageManager reinstalls dependencies after a git
			// update and reports no-match itself, unlike the legacy path.
			if err := newManager(cwd, agentDir, settings).Update(target.Source); err != nil {
				return err
			}
		} else {
			matched, err := updateExtensionPackages(target.Source, settings)
			if err != nil {
				return err
			}
			if target.Source != "" && !matched {
				return fmt.Errorf("no matching package found for %s", target.Source)
			}
		}
		if target.Source != "" {
			fmt.Println("Updated", target.Source)
		} else {
			fmt.Println("Updated packages")
		}
	}
	if target.Kind == "all" {
		return runSelfUpdate()
	}
	if target.Kind == "self" {
		return runSelfUpdate()
	}
	return nil
}

func runSelfUpdate() error {
	display, name, args, err := selfUpdateCommand()
	if err != nil {
		return err
	}
	fmt.Println("Updating pi with", display+"...")
	if err := runPackageCommand(name, args...); err != nil {
		return fmt.Errorf("self-update failed with %s: %w", display, err)
	}
	fmt.Println("Updated pi. If your shell still finds an older binary, check PATH or move the new binary into place.")
	return nil
}

func selfUpdateCommand() (string, string, []string, error) {
	if override := strings.TrimSpace(os.Getenv("PI_SELF_UPDATE_COMMAND")); override != "" {
		fields := strings.Fields(override)
		if len(fields) == 0 {
			return "", "", nil, fmt.Errorf("PI_SELF_UPDATE_COMMAND is empty")
		}
		return override, fields[0], fields[1:], nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return "", "", nil, fmt.Errorf("self-update requires a Go toolchain; run: go install github.com/guanshan/pi-go/cmd/pi@latest")
	}
	args := []string{"install", "github.com/guanshan/pi-go/cmd/pi@latest"}
	return "go " + strings.Join(args, " "), "go", args, nil
}

func updateExtensionPackages(source string, settings *SettingsManager) (bool, error) {
	records := append(packageRecords(settings, settings.CWD, settings.AgentDir, false), packageRecords(settings, settings.CWD, settings.AgentDir, true)...)
	matched := false
	for _, r := range records {
		if source != "" && !packageSourcesMatch(settings, r.Source, source, r.Local) {
			continue
		}
		matched = true
		if r.Pinned {
			fmt.Println("Skipping pinned package", r.Source)
			continue
		}
		if fileExists(filepath.Join(r.Path, ".git")) {
			if err := runPackageCommandIn(r.Path, "git", "pull", "--ff-only"); err != nil {
				return matched, err
			}
			fmt.Println("Updated", r.Source)
		}
	}
	return matched, nil
}

func removePackageSourceMatching(settings *SettingsManager, source string, local bool) bool {
	target := &settings.Global.Packages
	if local {
		target = &settings.Project.Packages
	}
	for i, record := range *target {
		if packageSourcesMatch(settings, record.Source, source, local) {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func removeInstalledPackageMatching(settings *SettingsManager, source string, local bool) bool {
	target := &settings.Global.InstalledPackages
	if local {
		target = &settings.Project.InstalledPackages
	}
	for i, record := range *target {
		if packageSourcesMatch(settings, record.Source, source, local) {
			*target = append((*target)[:i], (*target)[i+1:]...)
			return true
		}
	}
	return false
}

func packageSourcesMatch(settings *SettingsManager, existing, input string, local bool) bool {
	return packageSourceMatchKey(settings, existing, local, true) == packageSourceMatchKey(settings, input, local, false)
}

func packageSourceMatchKey(settings *SettingsManager, source string, local bool, settingsSource bool) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(source, "npm:") {
		name := strings.TrimPrefix(source, "npm:")
		name, _, _ = parsePackageSourceRef(name)
		return "npm:" + name
	}
	baseDir := settings.CWD
	if settingsSource {
		baseDir = settings.AgentDir
		if local {
			baseDir = ProjectPiDir(settings.CWD)
		}
	}
	if isManagedPackageSource(source) {
		return "managed:" + source
	}
	return "local:" + ResolveInCWD(baseDir, source)
}

func parsePackageSourceRef(name string) (base string, ref string, pinned bool) {
	if idx := strings.LastIndex(name, "@"); idx > 0 && !strings.Contains(name[idx:], "/") {
		return name[:idx], name[idx+1:], true
	}
	return name, "", false
}

func packageRecords(settings *SettingsManager, cwd, agentDir string, local bool) []PackageRecord {
	entries := packageEntries(settings, cwd, agentDir, local)
	records := make([]PackageRecord, 0, len(entries))
	for _, entry := range entries {
		records = append(records, entry.Record)
	}
	return records
}

func packageEntries(settings *SettingsManager, cwd, agentDir string, local bool) []packageEntry {
	if settings == nil {
		return nil
	}
	var entries []packageEntry
	seen := map[string]bool{}
	for _, source := range settings.PackageSources(local) {
		source.Source = strings.TrimSpace(source.Source)
		if source.Source == "" || seen[source.Source] {
			continue
		}
		seen[source.Source] = true
		entries = append(entries, packageEntry{
			Record: PackageRecord{
				Source: source.Source,
				Path:   packageInstallPath(source.Source, local, cwd, agentDir),
				Local:  local,
				Pinned: packageSourcePinned(source.Source),
			},
			Setting:  source,
			Filtered: packageSettingFiltered(source),
		})
	}
	for _, record := range settings.InstalledPackages(local) {
		record.Source = strings.TrimSpace(record.Source)
		if record.Source == "" || seen[record.Source] {
			continue
		}
		seen[record.Source] = true
		if record.Path == "" {
			record.Path = packageInstallPath(record.Source, local, cwd, agentDir)
		}
		entries = append(entries, packageEntry{Record: record})
	}
	return entries
}

func packageInstallPath(source string, local bool, cwd, agentDir string) string {
	if !isManagedPackageSource(source) {
		// Stored local sources are relative to the scope base directory, so
		// resolve from there (not cwd) to avoid drift when listing/updating from
		// a different working directory. Matches getBaseDirForScope().
		return ResolveInCWD(packageScopeBaseDir(cwd, agentDir, local), source)
	}
	root := filepath.Join(agentDir, "packages")
	if local {
		root = filepath.Join(ProjectPiDir(cwd), "packages")
	}
	return filepath.Join(root, sanitizeSource(source))
}

func isManagedPackageSource(source string) bool {
	return strings.HasPrefix(source, "npm:") ||
		strings.HasPrefix(source, "git:") ||
		strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "ssh://") ||
		strings.Contains(source, "github.com/")
}

func packageSourcePinned(source string) bool {
	if strings.HasPrefix(source, "git:") || strings.HasPrefix(source, "npm:") {
		source = strings.TrimPrefix(strings.TrimPrefix(source, "git:"), "npm:")
	}
	if idx := strings.LastIndex(source, "@"); idx > 0 && !strings.Contains(source[idx:], "/") {
		return true
	}
	return false
}

func packageSettingFiltered(source PackageSetting) bool {
	return source.Extensions != nil || source.Skills != nil || source.Prompts != nil || source.Themes != nil
}

func readPackageManifest(root string) PackageSetting {
	raw, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return PackageSetting{}
	}
	var pkg struct {
		Pi PackageSetting `json:"pi"`
	}
	if json.Unmarshal(raw, &pkg) != nil {
		return PackageSetting{}
	}
	return pkg.Pi
}

func runPackageCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runPackageCommandIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func sanitizeSource(source string) string {
	replacer := strings.NewReplacer("/", "-", ":", "-", "@", "-", "\\", "-")
	name := strings.Trim(replacer.Replace(source), "-")
	if name == "" {
		return "package"
	}
	return name
}
