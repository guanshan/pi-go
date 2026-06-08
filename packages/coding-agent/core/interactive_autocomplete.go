package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coreext "github.com/guanshan/pi-go/packages/coding-agent/core/extensions"
	"github.com/guanshan/pi-go/packages/tui"
)

func (m *interactiveModel) renderSuggestions() string {
	completions := m.currentCompletions()
	if len(completions) == 0 {
		return ""
	}
	values := completionValues(completions)
	selected := m.selectedSuggestionIndex(values)
	maxVisible := 5
	if agent, err := runtimeAgent(m.runtime); err == nil && agent != nil && agent.Settings != nil {
		maxVisible = agent.Settings.AutocompleteMaxVisible()
	}
	if maxVisible <= 0 {
		maxVisible = 5
	}
	half := maxVisible / 2
	start := selected - half
	if start > len(completions)-maxVisible {
		start = len(completions) - maxVisible
	}
	if start < 0 {
		start = 0
	}
	end := start + maxVisible
	if end > len(completions) {
		end = len(completions)
	}
	width := max(1, m.width)
	lines := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderCompletionLine(completions[i], i == selected, width))
	}
	if start > 0 || end < len(completions) {
		lines = append(lines, m.styles.Suggestion.Render(tui.TruncateToWidth(fmt.Sprintf("  (%d/%d)", selected+1, len(completions)), width, "...")))
	}
	return strings.Join(lines, "\n")
}

func (m *interactiveModel) renderCompletionLine(completion interactiveCompletion, selected bool, width int) string {
	prefix := "  "
	valueStyle := m.styles.Suggestion
	if selected {
		prefix = "> "
		valueStyle = m.styles.SelectorSelected
	}
	label := completion.Display()
	base := prefix + label
	if completion.Description == "" {
		return valueStyle.Render(tui.TruncateToWidth(base, width, "..."))
	}
	baseWidth := tui.VisibleWidth(base)
	if width-baseWidth < 12 {
		return valueStyle.Render(tui.TruncateToWidth(base, width, "..."))
	}
	descWidth := width - baseWidth - 2
	desc := tui.TruncateToWidth(completion.Description, descWidth, "...")
	return valueStyle.Render(base) + "  " + m.styles.SelectorDesc.Render(desc)
}

func (m *interactiveModel) completeSlashCommand() bool {
	completions := m.currentCompletions()
	if len(completions) == 0 {
		return false
	}
	value := m.input.Value()
	values := completionValues(completions)
	selected := completions[m.selectedSuggestionIndex(values)]
	if selected.Extension {
		if result, ok := m.applyExtensionCompletion(selected, value); ok {
			if completed, hasText := autocompleteApplyResultText(result); hasText {
				m.setInputValueAtCursor(completed, result.CursorLine, result.CursorCol)
				return true
			}
		}
		var completed string
		if selected.Prefix != "" && strings.HasSuffix(value, selected.Prefix) {
			completed = value[:len(value)-len(selected.Prefix)] + selected.Value
		} else {
			start := strings.LastIndexAny(value, " \t\r\n") + 1
			completed = value[:start] + selected.Value
		}
		if !completionIsDirectory(selected.Value) {
			completed += " "
		}
		m.input.SetValue(completed)
		m.input.MoveToEnd()
		return true
	}
	value = m.input.Value()
	selectedValue := selected.Value
	if _, start, ok := trailingFileRefToken(value); ok {
		// Replace just the trailing @token. A directory completion ends with "/"
		// so the user can keep descending without a separating space.
		completed := value[:start] + selectedValue
		if !completionIsDirectory(selectedValue) {
			completed += " "
		}
		m.input.SetValue(completed)
		m.input.MoveToEnd()
		return true
	}
	if _, start, ok := trailingPathCompletionToken(value); ok {
		completed := value[:start] + selectedValue
		if !completionIsDirectory(selectedValue) {
			completed += " "
		}
		m.input.SetValue(completed)
		m.input.MoveToEnd()
		return true
	}
	m.input.SetValue(selectedValue + " ")
	m.input.MoveToEnd()
	return true
}

func (m *interactiveModel) applyExtensionCompletion(completion interactiveCompletion, input string) (coreext.AutocompleteApplyResult, bool) {
	agent, _ := runtimeAgent(m.runtime)
	if agent == nil {
		return coreext.AutocompleteApplyResult{}, false
	}
	agent.mu.Lock()
	runtime := agent.extensionRuntime
	agent.mu.Unlock()
	if runtime == nil {
		return coreext.AutocompleteApplyResult{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	autocompleteRequest := autocompleteRequestFromInput(input)
	result, applied, err := runtime.ApplyAutocomplete(ctx, coreext.AutocompleteApplyRequest{
		Lines:      autocompleteRequest.Lines,
		CursorLine: autocompleteRequest.CursorLine,
		CursorCol:  autocompleteRequest.CursorCol,
		Input:      input,
		Cursor:     autocompleteRequest.Cursor,
		Item:       completion.Item,
		Prefix:     completion.Prefix,
	})
	if err != nil || !applied {
		return coreext.AutocompleteApplyResult{}, false
	}
	return result, true
}

func autocompleteApplyResultText(result coreext.AutocompleteApplyResult) (string, bool) {
	if len(result.Lines) > 0 {
		return strings.Join(result.Lines, "\n"), true
	}
	return result.Input, result.Input != ""
}

func (m *interactiveModel) setInputValueAtCursor(value string, cursorLine, cursorCol int) {
	m.input.SetValue(value)
	if cursorLine < 0 || cursorCol < 0 {
		m.input.MoveToEnd()
		return
	}
	m.input.MoveToBegin()
	for i := 0; i < cursorLine && m.input.Line() < m.input.LineCount()-1; i++ {
		m.input.CursorDown()
	}
	m.input.SetCursorColumn(cursorCol)
}

// trailingFileRefToken returns the final whitespace-delimited token of value, the
// byte index where it starts, and whether it is a file-reference token (begins
// with "@"). Used for @-attachment autocomplete.
func trailingFileRefToken(value string) (token string, start int, ok bool) {
	// An open @"..." quote lets a file reference contain spaces, so the token runs
	// from that @ to end-of-input rather than from the last whitespace (mirrors TS
	// findUnclosedQuoteStart/extractQuotedPrefix).
	if at := unclosedAtQuoteStart(value); at >= 0 {
		return value[at:], at, true
	}
	start = strings.LastIndexAny(value, " \t\r\n") + 1
	token = value[start:]
	return token, start, strings.HasPrefix(token, "@")
}

// unclosedAtQuoteStart returns the index of an '@' that opens an unclosed @"..."
// quote at a token boundary, or -1. Only @-prefixed quotes qualify (a bare
// unclosed quote is not a file reference).
func unclosedAtQuoteStart(value string) int {
	inQuotes := false
	quoteIdx := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteIdx = i
			}
		}
	}
	if !inQuotes || quoteIdx <= 0 || value[quoteIdx-1] != '@' {
		return -1
	}
	at := quoteIdx - 1
	if at == 0 || strings.ContainsRune(" \t\r\n", rune(value[at-1])) {
		return at
	}
	return -1
}

// fileReferenceSuggestions returns the completion values for the given
// "@<partial>" token (see fileReferenceCompletions for the full algorithm). It
// keeps the []string shape used by callers that only need replacement values.
func fileReferenceSuggestions(token, cwd string) []string {
	completions := fileReferenceCompletions(token, cwd)
	values := make([]string, 0, len(completions))
	for _, completion := range completions {
		values = append(values, completion.Value)
	}
	return values
}

// fileReferenceMaxResults caps @-attachment suggestions at the top 20 scored
// entries, mirroring TS getFuzzyFileSuggestions (autocomplete.ts:742).
const fileReferenceMaxResults = 20

// fileReferenceWalkLimit bounds how many filesystem entries the recursive walk
// considers before scoring, mirroring TS walkDirectoryWithFd's
// `fd --max-results 100` (autocomplete.ts:729).
const fileReferenceWalkLimit = 100

// fileReferenceCompletions performs a RECURSIVE FUZZY file search for the given
// "@<partial>" token, mirroring TS getFuzzyFileSuggestions (autocomplete.ts
// :717-769). It walks the whole subtree under the (optionally scoped) base
// directory, scores every entry (exact filename=100, prefix=80,
// substring-in-name=50, substring-in-path=30, +10 for directories), sorts by
// score descending, and returns the top 20 with a description carrying the
// display path. Directories get a trailing slash so the user can keep
// descending; values with spaces (or originally quoted tokens) are wrapped as
// @"...". Unlike a flat single-directory listing, a query like "@compon"
// surfaces "src/ui/components/" deep in the tree.
func fileReferenceCompletions(token, cwd string) []interactiveCompletion {
	rawPrefix := strings.TrimPrefix(token, "@")
	quoted := false
	if strings.HasPrefix(rawPrefix, "\"") {
		quoted = true
		rawPrefix = rawPrefix[1:]
	}
	normalizedQuery := filepath.ToSlash(rawPrefix)

	// Resolve a scoped query: if the typed text contains a "/", everything up to
	// and including the last "/" selects the base directory to walk, and only the
	// trailing segment is the fuzzy query (TS resolveScopedFuzzyQuery,
	// autocomplete.ts:518-546).
	scoped := false
	displayBase := ""
	query := normalizedQuery
	baseDir := cwd
	if slash := strings.LastIndex(normalizedQuery, "/"); slash >= 0 {
		displayBase = normalizedQuery[:slash+1]
		query = normalizedQuery[slash+1:]
		resolved := fileReferenceBaseDir(displayBase, cwd)
		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			scoped = true
			baseDir = resolved
		} else {
			// Not a real directory: fall back to walking cwd with the whole query,
			// matching TS (scopedQuery null -> fdBaseDir = basePath, fdQuery = query).
			scoped = false
			displayBase = ""
			query = normalizedQuery
			baseDir = cwd
		}
	}

	entries := fileReferenceWalk(baseDir, query, scoped)
	if len(entries) == 0 {
		return nil
	}

	type scoredEntry struct {
		relPath     string
		isDirectory bool
		score       int
	}
	scored := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		// Empty query scores every entry as 1 (TS getFuzzyFileSuggestions:737
		// `fdQuery ? scoreEntry(...) : 1`); a non-empty query uses the fuzzy score.
		score := 1
		if query != "" {
			score = scoreFileReferenceEntry(entry.relPath, query, entry.isDirectory)
		}
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredEntry{relPath: entry.relPath, isDirectory: entry.isDirectory, score: score})
	}
	// Stable sort by score descending so equal-score entries keep walk order
	// (TS Array.prototype.sort with (a,b)=>b.score-a.score is not stable in spec
	// but V8 is; stable Go sort matches the observable ordering).
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	if len(scored) > fileReferenceMaxResults {
		scored = scored[:fileReferenceMaxResults]
	}

	completions := make([]interactiveCompletion, 0, len(scored))
	for _, entry := range scored {
		displayPath := entry.relPath
		if scoped {
			displayPath = scopedFileReferenceDisplayPath(displayBase, entry.relPath)
		}
		completionPath := displayPath
		if entry.isDirectory {
			completionPath += "/"
		}
		value := buildFileRefCompletion(completionPath, quoted)
		label := filepathBase(entry.relPath)
		if entry.isDirectory {
			label += "/"
		}
		completions = append(completions, interactiveCompletion{
			Value:       value,
			Label:       label,
			Description: displayPath,
		})
	}
	return completions
}

// fileReferenceEntry is one path discovered by the recursive walk, relative to
// the base directory and with forward slashes.
type fileReferenceEntry struct {
	relPath     string
	isDirectory bool
}

// fileReferenceBaseDir resolves a scoped display base ("src/", "~/x/", "/abs/")
// to an absolute filesystem path (TS resolveScopedFuzzyQuery base resolution).
func fileReferenceBaseDir(displayBase, cwd string) string {
	switch {
	case strings.HasPrefix(displayBase, "~/"):
		home := HomeDir()
		if home == "" {
			return filepath.FromSlash(displayBase)
		}
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(displayBase, "~/")))
	case strings.HasPrefix(displayBase, "/"):
		return filepath.FromSlash(displayBase)
	case len(displayBase) >= 2 && displayBase[1] == ':':
		return filepath.FromSlash(displayBase)
	default:
		return filepath.Join(cwd, filepath.FromSlash(displayBase))
	}
}

// scopedFileReferenceDisplayPath re-prefixes a walk-relative path with the typed
// display base (TS scopedPathForDisplay, autocomplete.ts:548-554).
func scopedFileReferenceDisplayPath(displayBase, relPath string) string {
	relPath = filepath.ToSlash(relPath)
	if displayBase == "/" {
		return "/" + relPath
	}
	return filepath.ToSlash(displayBase) + relPath
}

// filepathBase returns the final path segment of a forward-slash relative path.
func filepathBase(relPath string) string {
	relPath = strings.TrimSuffix(filepath.ToSlash(relPath), "/")
	if i := strings.LastIndex(relPath, "/"); i >= 0 {
		return relPath[i+1:]
	}
	return relPath
}

// fileReferenceWalk recursively walks baseDir and returns up to
// fileReferenceWalkLimit entries (files and directories) relative to baseDir,
// mirroring TS walkDirectoryWithFd (fd --type f --type d --follow --hidden,
// .git excluded). Enumeration goes through interactiveReadDir so callers can
// observe/cache directory reads. Top-level dotfiles are hidden only for a bare,
// unscoped query that does not itself start with a dot (the Go #12 heuristic to
// avoid .git/node_modules noise); .git is always pruned, matching fd's
// --exclude .git.
func fileReferenceWalk(baseDir, query string, scoped bool) []fileReferenceEntry {
	showHidden := scoped || strings.HasPrefix(query, ".")
	results := make([]fileReferenceEntry, 0, 16)
	var walk func(absDir, relDir string)
	walk = func(absDir, relDir string) {
		if len(results) >= fileReferenceWalkLimit {
			return
		}
		entries, err := interactiveReadDir(absDir)
		if err != nil {
			return
		}
		// Deterministic traversal order (interactiveReadDir/os.ReadDir already
		// sorts by name, but be explicit so a swapped hook stays stable).
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if len(results) >= fileReferenceWalkLimit {
				return
			}
			name := entry.Name()
			if name == ".git" {
				continue
			}
			isDir := entry.IsDir()
			rel := name
			if relDir != "" {
				rel = relDir + "/" + name
			}
			hidden := strings.HasPrefix(name, ".")
			if hidden && !showHidden && relDir == "" {
				continue
			}
			results = append(results, fileReferenceEntry{relPath: rel, isDirectory: isDir})
			if isDir {
				walk(filepath.Join(absDir, name), rel)
			}
		}
	}
	walk(baseDir, "")
	return results
}

// scoreFileReferenceEntry scores a walk entry against the fuzzy query, mirroring
// TS scoreEntry (autocomplete.ts:692-714): exact filename match=100, filename
// prefix=80, substring in filename=50, substring in full path=30; directories
// with a positive score get a +10 bonus to sort first. 0 means no match.
func scoreFileReferenceEntry(relPath, query string, isDirectory bool) int {
	fileName := filepathBase(relPath)
	lowerName := strings.ToLower(fileName)
	lowerQuery := strings.ToLower(query)
	score := 0
	switch {
	case lowerName == lowerQuery:
		score = 100
	case strings.HasPrefix(lowerName, lowerQuery):
		score = 80
	case strings.Contains(lowerName, lowerQuery):
		score = 50
	case strings.Contains(strings.ToLower(filepath.ToSlash(relPath)), lowerQuery):
		score = 30
	}
	if isDirectory && score > 0 {
		score += 10
	}
	return score
}

// buildFileRefCompletion wraps a path as an "@"-prefixed completion value,
// quoting it as @"..." when the original token was quoted or the path contains a
// space (mirrors TS buildCompletionValue).
func buildFileRefCompletion(path string, quoted bool) string {
	if quoted || strings.Contains(path, " ") {
		if strings.HasSuffix(path, "/") {
			return "@\"" + path
		}
		return "@\"" + path + "\""
	}
	return "@" + path
}

func completionIsDirectory(value string) bool {
	return strings.HasSuffix(value, "/") || strings.HasSuffix(value, "/\"")
}

func trailingPathCompletionToken(value string) (token string, start int, ok bool) {
	if at := unclosedPlainQuoteStart(value); at >= 0 {
		return value[at:], at, true
	}
	start = strings.LastIndexAny(value, " \t\r\n=") + 1
	token = value[start:]
	return token, start, looksLikePathCompletionToken(token)
}

func unclosedPlainQuoteStart(value string) int {
	inQuotes := false
	quoteIdx := -1
	for i := 0; i < len(value); i++ {
		if value[i] == '"' {
			inQuotes = !inQuotes
			if inQuotes {
				quoteIdx = i
			}
		}
	}
	if !inQuotes || quoteIdx < 0 {
		return -1
	}
	if quoteIdx > 0 && value[quoteIdx-1] == '@' {
		return -1
	}
	if quoteIdx == 0 || strings.ContainsRune(" \t\r\n=", rune(value[quoteIdx-1])) {
		return quoteIdx
	}
	return -1
}

func looksLikePathCompletionToken(token string) bool {
	if token == "" || strings.HasPrefix(token, "@") {
		return false
	}
	return strings.HasPrefix(token, "\"") ||
		strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") ||
		strings.HasPrefix(token, "~/") ||
		strings.Contains(token, "/")
}

func pathCompletionSuggestions(token, cwd string) []string {
	rawPrefix := token
	quoted := false
	if strings.HasPrefix(rawPrefix, "\"") {
		quoted = true
		rawPrefix = rawPrefix[1:]
	}
	displayBase := ""
	readPrefix := rawPrefix
	if strings.HasPrefix(rawPrefix, "~/") {
		home := HomeDir()
		if home == "" {
			return nil
		}
		displayBase = "~/"
		readPrefix = filepath.Join(home, strings.TrimPrefix(rawPrefix, "~/"))
	}
	absolute := filepath.IsAbs(readPrefix)
	var dir, base string
	if slash := strings.LastIndex(readPrefix, "/"); slash >= 0 {
		dir, base = readPrefix[:slash], readPrefix[slash+1:]
		if dir == "" {
			dir = "/"
		}
	} else {
		dir, base = ".", readPrefix
	}
	readDir := dir
	if !absolute && displayBase == "" {
		readDir = filepath.Join(cwd, dir)
	}
	entries, err := interactiveReadDir(readDir)
	if err != nil {
		return nil
	}
	matches := make([]string, 0, 8)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") && dir == "." && !strings.HasPrefix(base, ".") {
			continue
		}
		if base != "" && !strings.HasPrefix(name, base) {
			continue
		}
		var rel string
		switch {
		case displayBase != "":
			typedDir := strings.TrimSuffix(strings.TrimPrefix(rawPrefix, "~/"), "/")
			if typedDir != "" && typedDir != base {
				parent := filepath.Dir(strings.TrimPrefix(rawPrefix, "~/"))
				if parent != "." {
					rel = displayBase + strings.TrimSuffix(parent, "/") + "/" + name
				} else {
					rel = displayBase + name
				}
			} else {
				rel = displayBase + name
			}
		case dir == "/":
			rel = "/" + name
		case dir != ".":
			rel = strings.TrimSuffix(dir, "/") + "/" + name
		default:
			rel = name
		}
		if entry.IsDir() {
			rel += "/"
		}
		matches = append(matches, buildPathCompletion(rel, quoted))
	}
	sort.Strings(matches)
	if len(matches) > 8 {
		matches = matches[:8]
	}
	return matches
}

func buildPathCompletion(path string, quoted bool) string {
	if quoted || strings.Contains(path, " ") {
		if strings.HasSuffix(path, "/") {
			return "\"" + path
		}
		return "\"" + path + "\""
	}
	return path
}

func slashCommandSuggestions(input string) []string {
	return interactiveSuggestions(input, nil)
}

type interactiveCompletion struct {
	Value       string
	Label       string
	Prefix      string
	Description string
	Extension   bool
	Item        coreext.AutocompleteItem
}

func (c interactiveCompletion) Display() string {
	if c.Label != "" {
		return c.Label
	}
	return c.Value
}

func (m *interactiveModel) currentSuggestions() []string {
	return completionValues(m.currentCompletions())
}

func (m *interactiveModel) currentCompletions() []interactiveCompletion {
	agent, _ := runtimeAgent(m.runtime)
	input := m.input.Value()
	builtins := m.builtinCompletionsFor(input, agent)
	return mergeInteractiveCompletions(builtins, m.extensionCompletionsFor(input, agent))
}

// extensionCompletionsFor memoizes the 250ms-bounded extension autocomplete RPC
// so currentCompletions/currentSuggestions reuse one result per input.
func (m *interactiveModel) extensionCompletionsFor(input string, agent *AgentSession) []interactiveCompletion {
	if m.extensionCompletionValid && m.extensionCompletionInput == input {
		return m.extensionCompletions
	}
	m.extensionCompletions = extensionAutocompleteCompletions(input, agent)
	m.extensionCompletionInput = input
	m.extensionCompletionValid = true
	return m.extensionCompletions
}

func completionsFromStrings(values []string) []interactiveCompletion {
	if len(values) == 0 {
		return nil
	}
	completions := make([]interactiveCompletion, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		completions = append(completions, interactiveCompletion{Value: value})
	}
	return completions
}

func completionValues(completions []interactiveCompletion) []string {
	if len(completions) == 0 {
		return nil
	}
	values := make([]string, 0, len(completions))
	for _, completion := range completions {
		if completion.Value != "" {
			values = append(values, completion.Value)
		}
	}
	return values
}

func mergeInteractiveCompletions(lists ...[]interactiveCompletion) []interactiveCompletion {
	var merged []interactiveCompletion
	seen := map[string]bool{}
	for _, list := range lists {
		for _, completion := range list {
			if completion.Value == "" || seen[completion.Value] {
				continue
			}
			seen[completion.Value] = true
			merged = append(merged, completion)
		}
	}
	return merged
}

func interactiveSuggestions(input string, agent *AgentSession) []string {
	return completionValues(interactiveBuiltinCompletions(input, agent))
}

func interactiveBuiltinCompletions(input string, agent *AgentSession) []interactiveCompletion {
	raw := strings.TrimLeft(input, " \t\r\n")
	text := strings.TrimSpace(input)
	// A trailing "@<partial>" token requests file-reference completion against the
	// session cwd (mirrors the TS @-attachment autocomplete), and may appear after
	// any text, including a slash command's arguments.
	if token, _, ok := trailingFileRefToken(input); ok {
		if agent != nil {
			if cwd := agent.Session.CWD(); cwd != "" {
				return fileReferenceCompletions(token, cwd)
			}
		}
		return nil
	}
	if strings.HasPrefix(raw, "/model ") {
		return modelCommandCompletions(raw, agent)
	}
	if token, _, ok := trailingPathCompletionToken(input); ok {
		if agent != nil {
			if cwd := agent.Session.CWD(); cwd != "" {
				if suggestions := pathCompletionSuggestions(token, cwd); len(suggestions) > 0 {
					return completionsFromStrings(suggestions)
				}
			}
		}
	}
	if !strings.HasPrefix(text, "/") || strings.Contains(text, " ") {
		if strings.HasPrefix(text, "/model ") {
			return modelCommandCompletions(text, agent)
		}
		return nil
	}
	prefix := strings.TrimPrefix(text, "/")
	// Mirror TS CombinedAutocompleteProvider: build the full command list, then
	// fuzzy-filter by command NAME ranked by match score (autocomplete.ts:322 via
	// fuzzyFilter). Subsequence matching makes "/mdl"->"/model" and
	// "/tngs"->"/settings" work, and orders results best-match first instead of
	// alphabetically. Candidates are deduped by value, preserving first-seen order
	// (built-ins, then templates, then skills, then extension commands) so equal
	// fuzzy scores fall back to that stable insertion order.
	candidates := make([]slashCommandCandidate, 0, 8)
	seen := map[string]bool{}
	add := func(name, value, description string) {
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		candidates = append(candidates, slashCommandCandidate{name: name, completion: interactiveCompletion{Value: value, Description: description}})
	}
	for _, command := range interactiveSlashCommands {
		add(command, "/"+command, interactiveSlashCommandDescriptions[command])
	}
	if agent != nil {
		// Iterate the template/skill maps in a stable (sorted) order so equal
		// fuzzy scores fall back to a deterministic ordering rather than Go's
		// randomized map iteration.
		templateNames := make([]string, 0, len(agent.Resources.PromptTemplates))
		for name := range agent.Resources.PromptTemplates {
			templateNames = append(templateNames, name)
		}
		sort.Strings(templateNames)
		for _, name := range templateNames {
			template := agent.Resources.PromptTemplates[name]
			add(name, "/"+name, prefixAutocompleteDescription(promptTemplateCompletionDescription(template), template.SourceInfo))
		}
		skillNames := make([]string, 0, len(agent.Resources.Skills))
		for name := range agent.Resources.Skills {
			skillNames = append(skillNames, name)
		}
		sort.Strings(skillNames)
		for _, name := range skillNames {
			command := "skill:" + name
			skill := agent.Resources.Skills[name]
			add(command, "/"+command, prefixAutocompleteDescription(skill.Description, skill.SourceInfo))
		}
		for _, extension := range extensionCommandCompletions(agent, "") {
			add(strings.TrimPrefix(extension.Value, "/"), extension.Value, extension.Description)
		}
	}
	filtered := tui.FuzzyFilter(candidates, prefix, func(c slashCommandCandidate) string { return c.name })
	matches := make([]interactiveCompletion, 0, len(filtered))
	for _, candidate := range filtered {
		matches = append(matches, candidate.completion)
	}
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}

// slashCommandCandidate pairs a slash command's match key (its name without the
// leading "/") with its completion, so tui.FuzzyFilter can rank by name while
// the completion's Value/Description ride along.
type slashCommandCandidate struct {
	name       string
	completion interactiveCompletion
}

func extensionAutocompleteCompletions(input string, agent *AgentSession) []interactiveCompletion {
	if agent == nil || input == "" {
		return nil
	}
	agent.mu.Lock()
	runtime := agent.extensionRuntime
	agent.mu.Unlock()
	if runtime == nil || len(runtime.RegisteredAutocompleteProviders()) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	result, err := runtime.Autocomplete(ctx, autocompleteRequestFromInput(input))
	if err != nil && len(result.Items) == 0 {
		return nil
	}
	completions := make([]interactiveCompletion, 0, len(result.Items))
	for _, item := range result.Items {
		value := item.Value
		if value == "" {
			value = item.Label
		}
		if value == "" {
			continue
		}
		completions = append(completions, interactiveCompletion{
			Value:       value,
			Label:       item.Label,
			Prefix:      result.Prefix,
			Description: item.Description,
			Extension:   true,
			Item:        item,
		})
	}
	return completions
}

func extensionCommandCompletions(agent *AgentSession, prefix string) []interactiveCompletion {
	if agent == nil {
		return nil
	}
	agent.mu.Lock()
	runtime := agent.extensionRuntime
	agent.mu.Unlock()
	if runtime == nil {
		return nil
	}
	builtin := map[string]bool{}
	for _, command := range interactiveSlashCommands {
		builtin[command] = true
	}
	var matches []interactiveCompletion
	for _, command := range runtime.RegisteredCommands() {
		name := strings.TrimSpace(command.Name)
		if name == "" || builtin[name] || !strings.HasPrefix(name, prefix) {
			continue
		}
		matches = append(matches, interactiveCompletion{
			Value:       "/" + name,
			Description: prefixAutocompleteDescription(command.Description, autocompleteSourceFromRaw(command.Source)),
		})
	}
	return matches
}

func promptTemplateCompletionDescription(template PromptTemplate) string {
	content := strings.TrimSpace(template.Content)
	if content == "" {
		return ""
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return line
	}
	return ""
}

func prefixAutocompleteDescription(description string, source ResourceSourceInfo) string {
	tag := autocompleteSourceTag(source)
	if tag == "" {
		return description
	}
	if description == "" {
		return "[" + tag + "]"
	}
	return "[" + tag + "] " + description
}

func autocompleteSourceTag(source ResourceSourceInfo) string {
	if strings.TrimSpace(source.Source) == "" && strings.TrimSpace(source.Scope) == "" {
		return ""
	}
	scopePrefix := "t"
	switch source.Scope {
	case "user":
		scopePrefix = "u"
	case "project":
		scopePrefix = "p"
	}
	raw := strings.TrimSpace(source.Source)
	if raw == "" || raw == "auto" || raw == "local" || raw == "cli" {
		return scopePrefix
	}
	if strings.HasPrefix(raw, "npm:") {
		return scopePrefix + ":" + raw
	}
	if git := autocompleteGitSourceTag(raw); git != "" {
		return scopePrefix + ":git:" + git
	}
	return scopePrefix
}

func autocompleteSourceFromRaw(source string) ResourceSourceInfo {
	source = strings.TrimSpace(source)
	if source == "" || source == "extension" {
		return ResourceSourceInfo{}
	}
	return ResourceSourceInfo{Source: source, Scope: "temporary"}
}

func autocompleteGitSourceTag(source string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(source), "git:")
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	raw = strings.TrimPrefix(raw, "ssh://git@")
	raw = strings.TrimPrefix(raw, "git@")
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "github.com:") {
		raw = "github.com/" + strings.TrimPrefix(raw, "github.com:")
	}
	if !strings.Contains(raw, "github.com/") && !strings.Contains(raw, "gitlab.com/") && !strings.Contains(raw, "bitbucket.org/") {
		return ""
	}
	if at := strings.LastIndex(raw, "@"); at > 0 && !strings.Contains(raw[at:], "/") {
		return raw[:at] + "@" + raw[at+1:]
	}
	return raw
}

func autocompleteRequestFromInput(input string) coreext.AutocompleteRequest {
	lines := strings.Split(input, "\n")
	cursorLine := max(0, len(lines)-1)
	cursorCol := 0
	if cursorLine < len(lines) {
		cursorCol = len([]rune(lines[cursorLine]))
	}
	return coreext.AutocompleteRequest{
		Lines:      lines,
		CursorLine: cursorLine,
		CursorCol:  cursorCol,
		Input:      input,
		Cursor:     len([]rune(input)),
	}
}

func (m *interactiveModel) selectedSuggestionIndex(suggestions []string) int {
	if len(suggestions) == 0 {
		m.autocompleteIndex = 0
		return 0
	}
	if m.autocompleteIndex < 0 {
		m.autocompleteIndex = 0
	}
	if m.autocompleteIndex >= len(suggestions) {
		m.autocompleteIndex = len(suggestions) - 1
	}
	return m.autocompleteIndex
}

func (m *interactiveModel) navigateAutocomplete(delta int) bool {
	suggestions := m.currentSuggestions()
	if len(suggestions) == 0 {
		m.autocompleteIndex = 0
		return false
	}
	index := m.selectedSuggestionIndex(suggestions)
	index = (index + delta) % len(suggestions)
	if index < 0 {
		index += len(suggestions)
	}
	m.autocompleteIndex = index
	m.historyIndex = -1
	return true
}

func modelCommandCompletions(text string, agent *AgentSession) []interactiveCompletion {
	if agent == nil {
		return nil
	}
	prefix := strings.TrimSpace(strings.TrimPrefix(text, "/model"))
	prefix = strings.ToLower(prefix)
	models := agent.availableModels()
	if len(models) == 0 && agent.Registry != nil {
		models = agent.Registry.List("")
	}
	matches := make([]interactiveCompletion, 0, 6)
	for _, model := range models {
		label := model.Provider + "/" + model.ID
		search := strings.ToLower(model.ID + " " + model.Provider + " " + label)
		if prefix == "" || strings.Contains(search, prefix) {
			matches = append(matches, interactiveCompletion{
				Value:       "/model " + label,
				Description: model.Provider,
			})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Value < matches[j].Value })
	if len(matches) > 6 {
		matches = matches[:6]
	}
	return matches
}
