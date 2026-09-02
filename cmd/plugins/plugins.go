// Package plugins exposes the installed Codex plugin and marketplace lifecycle
// through EveryAPI's CLI. Desktop clients consume this normalized JSON contract
// instead of spawning Codex or parsing plugin manifests themselves.
package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/everyapi-ai/everyapi-ai/v3/internal/cliout"
)

const (
	usage                           = "usage: everyapi plugins <catalog|list|install|remove|marketplace> [args] [--json]"
	marketplaceUsage                = "usage: everyapi plugins marketplace <list|add|update|remove> [args] [--json]"
	listTimeout                     = 20 * time.Second
	actionTimeout                   = 120 * time.Second
	maxOutputBytes                  = 4 * 1024 * 1024
	maxErrorRunes                   = 2_000
	maxManifestBytes          int64 = 512 * 1024
	maxPluginSelectorRunes          = 256
	maxMarketplaceSourceRunes       = 2_048
	maxSparsePaths                  = 32
)

type pluginSource struct {
	Source   string  `json:"source"`
	Path     *string `json:"path"`
	URL      *string `json:"url"`
	Package  *string `json:"package"`
	Registry *string `json:"registry"`
}

type codexPlugin struct {
	PluginID        string       `json:"pluginId"`
	Name            string       `json:"name"`
	MarketplaceName string       `json:"marketplaceName"`
	Version         *string      `json:"version"`
	Installed       bool         `json:"installed"`
	Enabled         bool         `json:"enabled"`
	Source          pluginSource `json:"source"`
	InstallPolicy   string       `json:"installPolicy"`
	AuthPolicy      string       `json:"authPolicy"`
}

type codexPluginList struct {
	Installed []codexPlugin `json:"installed"`
	Available []codexPlugin `json:"available"`
}

type codexMarketplace struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

type codexMarketplaceList struct {
	Marketplaces []codexMarketplace `json:"marketplaces"`
}

type pluginAuthor struct {
	Name *string `json:"name"`
}

type pluginInterface struct {
	DisplayName      *string  `json:"displayName"`
	ShortDescription *string  `json:"shortDescription"`
	LongDescription  *string  `json:"longDescription"`
	DeveloperName    *string  `json:"developerName"`
	Category         *string  `json:"category"`
	Capabilities     []string `json:"capabilities"`
	DefaultPrompt    any      `json:"defaultPrompt"`
	BrandColor       *string  `json:"brandColor"`
	WebsiteURL       *string  `json:"websiteURL"`
}

type pluginManifest struct {
	Description *string          `json:"description"`
	Homepage    *string          `json:"homepage"`
	Repository  any              `json:"repository"`
	Skills      any              `json:"skills"`
	MCPServers  any              `json:"mcpServers"`
	Apps        any              `json:"apps"`
	Author      *pluginAuthor    `json:"author"`
	Interface   *pluginInterface `json:"interface"`
}

// Plugin is the stable, presentation-ready contract returned to callers.
type Plugin struct {
	PluginID       string   `json:"plugin_id"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"display_name"`
	Marketplace    string   `json:"marketplace_name"`
	Version        *string  `json:"version"`
	Installed      bool     `json:"installed"`
	Enabled        bool     `json:"enabled"`
	SourceKind     string   `json:"source_kind"`
	SourceLocation *string  `json:"source_location"`
	InstallPolicy  string   `json:"install_policy"`
	AuthPolicy     string   `json:"auth_policy"`
	Summary        string   `json:"summary"`
	Description    string   `json:"description"`
	DeveloperName  *string  `json:"developer_name"`
	Category       *string  `json:"category"`
	Capabilities   []string `json:"capabilities"`
	DefaultPrompts []string `json:"default_prompts"`
	BrandColor     *string  `json:"brand_color"`
	Homepage       *string  `json:"homepage"`
	Repository     *string  `json:"repository"`
	SkillCount     int      `json:"skill_count"`
	HasMCPServer   bool     `json:"has_mcp_server"`
	HasApp         bool     `json:"has_app"`
}

// Marketplace is a configured Codex plugin marketplace.
type Marketplace struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

// Catalog is the complete plugin-management projection used by desktop clients.
type Catalog struct {
	Plugins      []Plugin      `json:"plugins"`
	Marketplaces []Marketplace `json:"marketplaces"`
}

type codexInvoker func(context.Context, []string, time.Duration, any) error

var invokeCodex codexInvoker = invokeCodexCommand

// Run dispatches `everyapi plugins`.
func Run(args []string) error {
	if len(args) == 0 || isHelp(args[0]) {
		cliout.Println(usage)
		return nil
	}
	switch args[0] {
	case "catalog":
		return runCatalog(args[1:])
	case "list":
		return runList(args[1:])
	case "install":
		return runInstall(args[1:])
	case "remove":
		return runRemove(args[1:])
	case "marketplace", "marketplaces":
		return runMarketplace(args[1:])
	default:
		return fmt.Errorf("unknown 'plugins' subcommand %q — try `everyapi plugins help`", args[0])
	}
}

func runCatalog(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", rest)
	}
	catalog, err := loadCatalog(context.Background())
	if err != nil {
		return err
	}
	return printCatalog(catalog, asJSON)
}

func runList(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	includeAvailable := false
	filtered := rest[:0]
	for _, arg := range rest {
		if arg == "--available" {
			includeAvailable = true
			continue
		}
		filtered = append(filtered, arg)
	}
	if len(filtered) != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", filtered)
	}
	list, err := loadPluginList(context.Background())
	if err != nil {
		return err
	}
	items := append([]codexPlugin(nil), list.Installed...)
	if includeAvailable {
		items = append(items, list.Available...)
	}
	plugins := enrichPlugins(items)
	if asJSON {
		return printJSON(struct {
			Plugins []Plugin `json:"plugins"`
		}{Plugins: plugins})
	}
	if len(plugins) == 0 {
		cliout.Println("No plugins found.")
		return nil
	}
	for _, plugin := range plugins {
		state := "available"
		if plugin.Installed {
			state = "installed"
		}
		cliout.Printf("%-36s  %-10s  %s\n", cliout.Sanitize(plugin.PluginID), state, cliout.Sanitize(plugin.DisplayName))
	}
	return nil
}

func runInstall(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) != 1 {
		return errors.New("usage: everyapi plugins install <plugin@marketplace> [--json]")
	}
	selector, err := validatePluginSelector(rest[0])
	if err != nil {
		return err
	}
	ctx := context.Background()
	list, err := loadPluginList(ctx)
	if err != nil {
		return err
	}
	if !pluginMatches(list.Available, selector, false) && !pluginMatches(list.Installed, selector, false) {
		return errors.New("plugin is not available for installation")
	}
	if err := invokeCodex(ctx, []string{"plugin", "add", "--json", selector}, actionTimeout, &struct{}{}); err != nil {
		return err
	}
	return printMutationResult(ctx, asJSON, fmt.Sprintf("Installed %s.", selector))
}

func runRemove(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) != 1 {
		return errors.New("usage: everyapi plugins remove <plugin@marketplace> [--json]")
	}
	selector, err := validatePluginSelector(rest[0])
	if err != nil {
		return err
	}
	ctx := context.Background()
	list, err := loadPluginList(ctx)
	if err != nil {
		return err
	}
	if !pluginMatches(list.Installed, selector, true) && !pluginMatches(list.Available, selector, true) {
		return errors.New("plugin is not installed")
	}
	if err := invokeCodex(ctx, []string{"plugin", "remove", "--json", selector}, actionTimeout, &struct{}{}); err != nil {
		return err
	}
	return printMutationResult(ctx, asJSON, fmt.Sprintf("Removed %s.", selector))
}

func runMarketplace(args []string) error {
	if len(args) == 0 || isHelp(args[0]) {
		cliout.Println(marketplaceUsage)
		return nil
	}
	switch args[0] {
	case "list":
		return runMarketplaceList(args[1:])
	case "add":
		return runMarketplaceAdd(args[1:])
	case "update", "upgrade":
		return runMarketplaceUpdate(args[1:])
	case "remove":
		return runMarketplaceRemove(args[1:])
	default:
		return fmt.Errorf("unknown plugin marketplace subcommand %q", args[0])
	}
}

func runMarketplaceList(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", rest)
	}
	list, err := loadMarketplaceList(context.Background())
	if err != nil {
		return err
	}
	marketplaces := projectMarketplaces(list)
	if asJSON {
		return printJSON(struct {
			Marketplaces []Marketplace `json:"marketplaces"`
		}{Marketplaces: marketplaces})
	}
	if len(marketplaces) == 0 {
		cliout.Println("No plugin marketplaces configured.")
		return nil
	}
	for _, marketplace := range marketplaces {
		cliout.Printf("%-28s  %s\n", cliout.Sanitize(marketplace.Name), cliout.Sanitize(marketplace.Root))
	}
	return nil
}

func runMarketplaceAdd(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	var refName string
	var sparse []string
	var source string
	for index := 0; index < len(rest); index++ {
		switch rest[index] {
		case "--ref":
			index++
			if index >= len(rest) {
				return errors.New("--ref requires a value")
			}
			refName = rest[index]
		case "--sparse":
			index++
			if index >= len(rest) {
				return errors.New("--sparse requires a value")
			}
			sparse = append(sparse, rest[index])
		default:
			if strings.HasPrefix(rest[index], "-") || source != "" {
				return fmt.Errorf("unexpected marketplace argument %q", rest[index])
			}
			source = rest[index]
		}
	}
	validatedSource, err := validateMarketplaceSource(source)
	if err != nil {
		return err
	}
	if len(sparse) > maxSparsePaths {
		return fmt.Errorf("at most %d sparse checkout paths are allowed", maxSparsePaths)
	}
	command := []string{"plugin", "marketplace", "add", "--json"}
	if refName != "" {
		value, err := validateRefName(refName)
		if err != nil {
			return err
		}
		command = append(command, "--ref", value)
	}
	for _, path := range sparse {
		value, err := validateSparsePath(path)
		if err != nil {
			return err
		}
		command = append(command, "--sparse", value)
	}
	command = append(command, validatedSource)
	if err := invokeCodex(context.Background(), command, actionTimeout, &struct{}{}); err != nil {
		return err
	}
	return printMutationResult(context.Background(), asJSON, "Plugin marketplace added.")
}

func runMarketplaceUpdate(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) > 1 {
		return errors.New("usage: everyapi plugins marketplace update [name] [--json]")
	}
	ctx := context.Background()
	command := []string{"plugin", "marketplace", "upgrade", "--json"}
	if len(rest) == 1 {
		name, err := validateMarketplaceName(rest[0])
		if err != nil {
			return err
		}
		list, err := loadMarketplaceList(ctx)
		if err != nil {
			return err
		}
		if !marketplaceExists(list, name) {
			return errors.New("plugin marketplace is no longer configured")
		}
		command = append(command, name)
	}
	if err := invokeCodex(ctx, command, actionTimeout, &struct{}{}); err != nil {
		return err
	}
	return printMutationResult(ctx, asJSON, "Plugin marketplaces updated.")
}

func runMarketplaceRemove(args []string) error {
	rest, asJSON := stripJSONFlag(args)
	if len(rest) != 1 {
		return errors.New("usage: everyapi plugins marketplace remove <name> [--json]")
	}
	name, err := validateMarketplaceName(rest[0])
	if err != nil {
		return err
	}
	ctx := context.Background()
	list, err := loadMarketplaceList(ctx)
	if err != nil {
		return err
	}
	if !marketplaceExists(list, name) {
		return errors.New("plugin marketplace is no longer configured")
	}
	if err := invokeCodex(ctx, []string{"plugin", "marketplace", "remove", "--json", name}, actionTimeout, &struct{}{}); err != nil {
		return err
	}
	return printMutationResult(ctx, asJSON, fmt.Sprintf("Removed marketplace %s.", name))
}

func printMutationResult(ctx context.Context, asJSON bool, message string) error {
	if !asJSON {
		cliout.Println(message)
		return nil
	}
	catalog, err := loadCatalog(ctx)
	if err != nil {
		return err
	}
	return printJSON(catalog)
}

func printCatalog(catalog Catalog, asJSON bool) error {
	if asJSON {
		return printJSON(catalog)
	}
	cliout.Printf("%d plugins · %d marketplaces\n", len(catalog.Plugins), len(catalog.Marketplaces))
	for _, plugin := range catalog.Plugins {
		state := "available"
		if plugin.Installed {
			state = "installed"
		}
		cliout.Printf("%-36s  %-10s  %s\n", cliout.Sanitize(plugin.PluginID), state, cliout.Sanitize(plugin.DisplayName))
	}
	return nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(cliout.Out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func loadCatalog(ctx context.Context) (Catalog, error) {
	plugins, err := loadPluginList(ctx)
	if err != nil {
		return Catalog{}, err
	}
	marketplaces, err := loadMarketplaceList(ctx)
	if err != nil {
		return Catalog{}, err
	}
	return Catalog{
		Plugins:      enrichPlugins(append(append([]codexPlugin(nil), plugins.Installed...), plugins.Available...)),
		Marketplaces: projectMarketplaces(marketplaces),
	}, nil
}

func loadPluginList(ctx context.Context) (codexPluginList, error) {
	var list codexPluginList
	err := invokeCodex(ctx, []string{"plugin", "list", "--available", "--json"}, listTimeout, &list)
	return list, err
}

func loadMarketplaceList(ctx context.Context) (codexMarketplaceList, error) {
	var list codexMarketplaceList
	err := invokeCodex(ctx, []string{"plugin", "marketplace", "list", "--json"}, listTimeout, &list)
	return list, err
}

func enrichPlugins(items []codexPlugin) []Plugin {
	plugins := make([]Plugin, 0, len(items))
	for _, item := range items {
		plugins = append(plugins, enrichPlugin(item))
	}
	sort.SliceStable(plugins, func(left, right int) bool {
		if plugins[left].Installed != plugins[right].Installed {
			return plugins[left].Installed
		}
		leftName := strings.ToLower(plugins[left].DisplayName)
		rightName := strings.ToLower(plugins[right].DisplayName)
		if leftName != rightName {
			return leftName < rightName
		}
		return plugins[left].PluginID < plugins[right].PluginID
	})
	return plugins
}

func enrichPlugin(item codexPlugin) Plugin {
	location := firstString(item.Source.Path, item.Source.URL, item.Source.Package, item.Source.Registry)
	manifest, root := readManifest(item.Source.Path)
	var iface *pluginInterface
	if manifest != nil {
		iface = manifest.Interface
	}
	displayName := item.Name
	if iface != nil {
		displayName = cleanText(iface.DisplayName, 160, displayName)
	}
	summary := ""
	description := ""
	if iface != nil {
		summary = cleanText(iface.ShortDescription, 500, "")
		description = cleanText(iface.LongDescription, 4_000, "")
	}
	if manifest != nil {
		if summary == "" {
			summary = cleanText(manifest.Description, 500, "")
		}
		if description == "" {
			description = cleanText(manifest.Description, 4_000, "")
		}
	}
	if description == "" {
		description = summary
	}
	plugin := Plugin{
		PluginID:       item.PluginID,
		Name:           item.Name,
		DisplayName:    displayName,
		Marketplace:    item.MarketplaceName,
		Version:        item.Version,
		Installed:      item.Installed,
		Enabled:        item.Enabled,
		SourceKind:     item.Source.Source,
		SourceLocation: location,
		InstallPolicy:  item.InstallPolicy,
		AuthPolicy:     item.AuthPolicy,
		Summary:        summary,
		Description:    description,
		Capabilities:   []string{},
		DefaultPrompts: []string{},
	}
	if iface != nil {
		plugin.DeveloperName = cleanTextPointer(iface.DeveloperName, 160)
		plugin.Category = cleanTextPointer(iface.Category, 120)
		plugin.Capabilities = cleanStrings(iface.Capabilities, 24, 80)
		plugin.DefaultPrompts = promptStrings(iface.DefaultPrompt)
		plugin.BrandColor = validBrandColor(iface.BrandColor)
		plugin.Homepage = cleanURL(iface.WebsiteURL)
	}
	if manifest != nil {
		if plugin.DeveloperName == nil && manifest.Author != nil {
			plugin.DeveloperName = cleanTextPointer(manifest.Author.Name, 160)
		}
		if plugin.Homepage == nil {
			plugin.Homepage = cleanURL(manifest.Homepage)
		}
		plugin.Repository = cleanURL(jsonString(manifest.Repository))
		plugin.SkillCount = countManifestSkills(root, manifest.Skills)
		plugin.HasMCPServer = componentExists(root, manifest.MCPServers, ".mcp.json", true)
		plugin.HasApp = componentExists(root, manifest.Apps, ".app.json", false)
	}
	return plugin
}

func readManifest(sourcePath *string) (*pluginManifest, string) {
	if sourcePath == nil || *sourcePath == "" {
		return nil, ""
	}
	root, err := filepath.EvalSymlinks(*sourcePath)
	if err != nil {
		return nil, ""
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, ""
	}
	path := filepath.Join(root, ".codex-plugin", "plugin.json")
	path, err = filepath.EvalSymlinks(path)
	if err != nil || !pathWithin(root, path) {
		return nil, ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return nil, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ""
	}
	var manifest pluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, ""
	}
	return &manifest, root
}

func countManifestSkills(root string, value any) int {
	path := jsonString(value)
	if root == "" || path == nil {
		return 0
	}
	directory, ok := resolveComponentPath(root, *path)
	if !ok {
		return 0
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	count := 0
	for index, entry := range entries {
		if index >= 256 {
			break
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		if info, err := os.Stat(filepath.Join(directory, entry.Name(), "SKILL.md")); err == nil && info.Mode().IsRegular() {
			count++
		}
	}
	return count
}

func componentExists(root string, value any, defaultName string, allowObject bool) bool {
	if root == "" {
		return false
	}
	if allowObject {
		if object, ok := value.(map[string]any); ok && len(object) > 0 {
			return true
		}
	}
	path := defaultName
	if declared := jsonString(value); declared != nil {
		path = *declared
	}
	resolved, ok := resolveComponentPath(root, path)
	if !ok {
		return false
	}
	info, err := os.Stat(resolved)
	return err == nil && info.Mode().IsRegular()
}

func resolveComponentPath(root, value string) (string, bool) {
	if value == "" || filepath.IsAbs(value) {
		return "", false
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(value)))
	if err != nil || !pathWithin(root, path) {
		return "", false
	}
	return path, true
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func cleanText(value *string, limit int, fallback string) string {
	clean := cleanTextPointer(value, limit)
	if clean == nil {
		return fallback
	}
	return *clean
}

func cleanTextPointer(value *string, limit int) *string {
	if value == nil {
		return nil
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil
	}
	text = truncateRunes(text, limit)
	return &text
}

func cleanStrings(values []string, countLimit, runeLimit int) []string {
	clean := make([]string, 0, min(len(values), countLimit))
	for _, value := range values {
		if len(clean) >= countLimit {
			break
		}
		if item := cleanTextPointer(&value, runeLimit); item != nil {
			clean = append(clean, *item)
		}
	}
	return clean
}

func promptStrings(value any) []string {
	var values []string
	switch typed := value.(type) {
	case string:
		values = []string{typed}
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
	}
	return cleanStrings(values, 3, 128)
}

func cleanURL(value *string) *string {
	if value == nil {
		return nil
	}
	text := strings.TrimSpace(*value)
	if utf8.RuneCountInString(text) > maxMarketplaceSourceRunes {
		return nil
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil
	}
	return &text
}

func validBrandColor(value *string) *string {
	if value == nil {
		return nil
	}
	text := strings.ToUpper(strings.TrimSpace(*value))
	if len(text) != 4 && len(text) != 7 {
		return nil
	}
	if text[0] != '#' {
		return nil
	}
	for _, character := range text[1:] {
		if !strings.ContainsRune("0123456789ABCDEF", character) {
			return nil
		}
	}
	return &text
}

func jsonString(value any) *string {
	switch typed := value.(type) {
	case string:
		return &typed
	case map[string]any:
		if text, ok := typed["url"].(string); ok {
			return &text
		}
	}
	return nil
}

func firstString(values ...*string) *string {
	for _, value := range values {
		if value != nil && *value != "" {
			copy := *value
			return &copy
		}
	}
	return nil
}

func pluginMatches(items []codexPlugin, selector string, installed bool) bool {
	for _, item := range items {
		if item.PluginID == selector && item.Installed == installed {
			return true
		}
	}
	return false
}

func marketplaceExists(list codexMarketplaceList, name string) bool {
	for _, marketplace := range list.Marketplaces {
		if marketplace.Name == name {
			return true
		}
	}
	return false
}

func projectMarketplaces(list codexMarketplaceList) []Marketplace {
	items := make([]Marketplace, 0, len(list.Marketplaces))
	for _, marketplace := range list.Marketplaces {
		items = append(items, Marketplace{Name: marketplace.Name, Root: marketplace.Root})
	}
	sort.SliceStable(items, func(left, right int) bool { return items[left].Name < items[right].Name })
	return items
}

func stripJSONFlag(args []string) ([]string, bool) {
	rest := make([]string, 0, len(args))
	asJSON := false
	for _, arg := range args {
		if arg == "--json" {
			asJSON = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, asJSON
}

func isHelp(value string) bool {
	return value == "help" || value == "--help" || value == "-h"
}

func validatePluginSelector(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxPluginSelectorRunes || strings.HasPrefix(value, "-") || strings.Count(value, "@") != 1 {
		return "", errors.New("invalid Codex plugin selector")
	}
	parts := strings.SplitN(value, "@", 2)
	if parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid Codex plugin selector")
	}
	for _, character := range value {
		if !isIdentifierRune(character) && character != '@' {
			return "", errors.New("invalid Codex plugin selector")
		}
	}
	return value, nil
}

func validateMarketplaceName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 160 || strings.HasPrefix(value, "-") {
		return "", errors.New("invalid Codex plugin marketplace name")
	}
	for _, character := range value {
		if !isIdentifierRune(character) {
			return "", errors.New("invalid Codex plugin marketplace name")
		}
	}
	return value, nil
}

func validateMarketplaceSource(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > maxMarketplaceSourceRunes || strings.HasPrefix(value, "-") || containsControl(value) {
		return "", errors.New("invalid Codex plugin marketplace source")
	}
	return value, nil
}

func validateRefName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 256 || strings.HasPrefix(value, "-") || containsControl(value) {
		return "", errors.New("invalid marketplace Git ref")
	}
	return value, nil
}

func validateSparsePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 512 || strings.HasPrefix(value, "-") || containsControl(value) {
		return "", errors.New("invalid marketplace sparse checkout path")
	}
	return value, nil
}

func isIdentifierRune(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '_' || value == '.'
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < ' ' || character == 127 {
			return true
		}
	}
	return false
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return 0, errors.New("plugin command output exceeded the limit")
	}
	if len(data) > remaining {
		buffer.exceeded = true
		written, _ := buffer.buffer.Write(data[:remaining])
		return written, errors.New("plugin command output exceeded the limit")
	}
	return buffer.buffer.Write(data)
}

func invokeCodexCommand(parent context.Context, args []string, timeout time.Duration, target any) error {
	program := os.Getenv("EVERYAPI_CODEX_CLI_PATH")
	if program == "" {
		program = "codex"
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Env = append(os.Environ(), "PAGER=cat", "GIT_TERMINAL_PROMPT=0")
	command.Stdin = nil
	stdout := &boundedBuffer{limit: maxOutputBytes}
	stderr := &boundedBuffer{limit: maxOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("Codex CLI timed out after %d seconds", int(timeout.Seconds()))
	}
	if stdout.exceeded || stderr.exceeded {
		return errors.New("Codex CLI output exceeded 4 MiB")
	}
	if err != nil {
		message := truncateRunes(strings.TrimSpace(stderr.buffer.String()), maxErrorRunes)
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("Codex CLI failed: %s", cliout.Sanitize(message))
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewReader(stdout.buffer.Bytes()), maxOutputBytes))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("Codex CLI returned invalid JSON: %w", err)
	}
	return nil
}
