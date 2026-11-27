package config

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const serverConfigHeader = "This node only: listen, identity, database, cache, ssl. The rest is in yaml.d/ and credentials are in secrets.yml."

var nodeConfigKeys = map[string]struct{}{
	"port": {}, "address": {}, "fqdn": {}, "mode": {}, "api_version": {}, "baseurl": {},
	"pidfile": {}, "daemonize": {}, "debug": {}, "database": {}, "cache": {}, "ssl": {}, "trusted_proxies": {},
}

var legacyAppSecrets = struct {
	sync.Mutex
	values map[string]string
}{values: make(map[string]string)}

// ConsumeLegacyApplicationSecrets transfers values that migration must store in app_secrets.
func ConsumeLegacyApplicationSecrets() map[string]string {
	legacyAppSecrets.Lock()
	defer legacyAppSecrets.Unlock()
	values := legacyAppSecrets.values
	legacyAppSecrets.values = make(map[string]string)
	return values
}

func saveNodeConfiguration(path string, cfg *Config) error {
	root, err := configYAMLNode(cfg)
	if err != nil {
		return err
	}
	server := yamlConfigMappingValues(root)["server"]
	for index := 0; index+1 < len(server.Content); {
		key := server.Content[index].Value
		if _, keep := nodeConfigKeys[key]; !keep {
			server.Content = append(server.Content[:index], server.Content[index+2:]...)
			continue
		}
		index += 2
	}
	root.HeadComment = serverConfigHeader
	data, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writeAtomicPrivateFile(path, data)
}

// SaveNodeConfiguration writes only node-owned settings to server.yml.
func SaveNodeConfiguration(path string, cfg *Config) error {
	return saveNodeConfiguration(path, cfg)
}

// SaveUpdateBranch stores the CLI's update-channel setting in its maintenance area file.
func SaveUpdateBranch(serverConfigPath, branch string) error {
	directory := filepath.Join(filepath.Dir(serverConfigPath), "yaml.d")
	if err := ensurePrivateYAMLDirectory(directory); err != nil {
		return err
	}
	path := filepath.Join(directory, "maintenance.yml")
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("maintenance.yml must be a regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document yaml.Node
		if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
			return fmt.Errorf("maintenance.yml must contain a valid YAML mapping")
		}
		root = document.Content[0]
	} else if !os.IsNotExist(err) {
		return err
	}
	setYAMLConfigPath(root, "server.update.branch", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: branch})
	data, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writeAtomicPrivateFile(path, data)
}

func migrateConfigLayout(path string) error {
	migrateYamlToYml(path)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("server configuration must be a regular file")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(original, &document); err != nil {
		return nil
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	root := document.Content[0]
	applyLegacyConfigMigrations(root)
	server := yamlConfigMappingValues(root)["server"]
	if server == nil || server.Kind != yaml.MappingNode {
		return nil
	}

	areas := map[string]*yaml.Node{}
	move := func(area, section string, value *yaml.Node) {
		if value == nil {
			return
		}
		if areas[area] == nil {
			areas[area] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
		setYAMLConfigPath(areas[area], section, cloneYAMLConfigNode(value))
	}
	for _, item := range []struct{ key, area string }{
		{"branding", "branding"},
		{"seo", "branding"},
		{"i18n", "web"},
		{"healthz", "web"},
		{"csrf", "web"},
		{"cors", "web"},
		{"compression", "web"},
		{"url_detection", "web"},
		{"pages", "pages"},
		{"contact", "contact"},
		{"privacy", "privacy"},
		{"compliance", "privacy"},
		{"tracking", "privacy"},
		{"security", "security"},
		{"rate_limit", "security"},
		{"geoip", "security"},
		{"limits", "security"},
		{"tokens", "security"},
		{"logs", "logs"},
		{"metrics", "metrics"},
		{"notifications", "notifications"},
		{"tor", "tor"},
		{"i2p", "i2p"},
		{"scheduler", "maintenance"},
		{"update", "maintenance"},
		{"auto_update", "maintenance"},
		{"backup", "maintenance"},
		{"restore", "maintenance"},
		{"maintenance", "maintenance"},
	} {
		value := mappingNodeValue(server, item.key)
		if value == nil {
			continue
		}
		move(item.area, "server."+item.key, value)
		removeYAMLConfigKey(server, item.key)
	}
	for index := 0; index+1 < len(server.Content); {
		key := server.Content[index].Value
		if _, nodeOwned := nodeConfigKeys[key]; nodeOwned {
			index += 2
			continue
		}
		move("app", "server."+key, server.Content[index+1])
		server.Content = append(server.Content[:index], server.Content[index+2:]...)
	}
	for _, item := range []struct {
		key  string
		area string
	}{
		{"data", "security"}, {"cli", "app"}, {"web", "web"}, {"web-robots", "web"}, {"web-security", "web"}, {"web-ui", "branding"},
	} {
		value := mappingNodeValue(root, item.key)
		if value == nil {
			continue
		}
		areaRoot := areas[item.area]
		if areaRoot == nil {
			areaRoot = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			areas[item.area] = areaRoot
		}
		if item.key == "web-ui" {
			web := mappingNodeValue(areaRoot, "web")
			if web == nil {
				web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				setYAMLConfigKey(areaRoot, "web", web)
			}
			setYAMLConfigKey(web, "ui", cloneYAMLConfigNode(value))
		} else {
			setYAMLConfigKey(areaRoot, item.key, cloneYAMLConfigNode(value))
		}
		removeYAMLConfigKey(root, item.key)
	}
	if web := mappingNodeValue(areas["web"], "web"); web != nil && web.Kind == yaml.MappingNode {
		if ui := mappingNodeValue(web, "ui"); ui != nil {
			branding := areas["branding"]
			if branding == nil {
				branding = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				areas["branding"] = branding
			}
			setYAMLConfigPath(branding, "web.ui", cloneYAMLConfigNode(ui))
			removeYAMLConfigKey(web, "ui")
		}
	}
	if value := mappingNodeValue(root, "tor"); value != nil {
		move("tor", "tor", value)
		removeYAMLConfigKey(root, "tor")
	}
	knownTopLevel := map[string]struct{}{"server": {}, "data": {}, "cli": {}, "web": {}, "web-robots": {}, "web-security": {}, "web-ui": {}, "tor": {}}
	for index := 0; index+1 < len(root.Content); {
		key := root.Content[index].Value
		if _, known := knownTopLevel[key]; known {
			index += 2
			continue
		}
		move("app", key, root.Content[index+1])
		root.Content = append(root.Content[:index], root.Content[index+2:]...)
	}
	secretsPath := filepath.Join(filepath.Dir(path), "secrets.yml")
	secrets, err := readExistingYAMLMapping(secretsPath)
	if err != nil {
		return fmt.Errorf("read secrets.yml before migration: %w", err)
	}
	secretCandidates := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	secretChanged := false
	for _, source := range append([]*yaml.Node{root}, areaNodesInMigrationOrder(areas)...) {
		secretChanged = extractConfigSecrets(source, secretCandidates) || secretChanged
	}
	configDir := filepath.Dir(path)
	dropInDir := filepath.Join(configDir, "yaml.d")
	changedDropIns, extractedFromDropIns := migrateSecretsFromDropIns(dropInDir, secretCandidates)
	secretChanged = secretChanged || extractedFromDropIns
	if len(areas) == 0 && !secretChanged && len(changedDropIns) == 0 {
		return nil
	}

	if err := ensurePrivateYAMLDirectory(dropInDir); err != nil {
		return err
	}
	backup := path + ".bak"
	if err := writeAtomicPrivateFile(backup, original); err != nil {
		return fmt.Errorf("write configuration migration backup: %w", err)
	}
	areaNames := []string{"branding", "web", "pages", "contact", "privacy", "security", "logs", "metrics", "notifications", "tor", "i2p", "maintenance", "app"}
	for _, name := range areaNames {
		area := areas[name]
		if area == nil {
			continue
		}
		target := filepath.Join(dropInDir, name+".yml")
		if existing := changedDropIns[target]; existing != nil {
			mergeMissingYAMLConfig(existing, area)
			continue
		}
		if err := mergeIntoExistingDropIn(target, area); err != nil {
			return fmt.Errorf("write yaml.d/%s.yml: %w", name, err)
		}
	}
	for path, node := range changedDropIns {
		data, err := yaml.Marshal(node)
		if err != nil {
			return err
		}
		if err := writeAtomicPrivateFile(path, data); err != nil {
			return fmt.Errorf("remove secret values from %s: %w", path, err)
		}
	}
	if secretChanged {
		mergeMissingYAMLConfig(secrets, secretCandidates)
		data, err := yaml.Marshal(secrets)
		if err != nil {
			return err
		}
		if err := writeAtomicPrivateFile(secretsPath, data); err != nil {
			return fmt.Errorf("write secrets.yml: %w", err)
		}
	}
	var output bytes.Buffer
	root.HeadComment = serverConfigHeader
	encoded, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	output.Write(encoded)
	return writeAtomicPrivateFile(path, output.Bytes())
}

// MigrateConfigLayout moves legacy settings after the service has dropped privileges.
func MigrateConfigLayout(path string) error {
	return migrateConfigLayout(path)
}

func applyLegacyConfigMigrations(root *yaml.Node) {
	if root == nil || root.Kind != yaml.MappingNode {
		return
	}
	move := func(from, to string) {
		if value := takeYAMLConfigPath(root, from); value != nil && mappingNodeValueAtPath(root, to) == nil {
			setYAMLConfigPath(root, to, value)
		}
	}
	remove := func(path string) { _ = takeYAMLConfigPath(root, path) }
	for _, pair := range [][2]string{
		{"server.logging", "server.logs"},
		{"server.schedule", "server.scheduler"},
		{"server.web.cookie_consent.message", "server.privacy.consent.message"},
		{"server.web.cookie_consent.policy_url", "server.privacy.consent.policy.url"},
		{"server.security_headers.hsts", "web.hsts.enabled"},
		{"server.security_headers.hsts_max_age", "web.hsts.max_age_seconds"},
		{"server.security.hsts", "web.hsts"},
		{"server.security.nel", "web.headers.nel"},
		{"server.security.headers.clear_site_data", "web.headers.clear_site_data"},
		{"server.security.csrf", "server.csrf"},
		{"web.csrf", "server.csrf"},
		{"database", "server.database"},
		{"server.title", "server.branding.title"},
		{"server.description", "server.branding.description"},
		{"server.seo.opengraph.image", "server.seo.og_image"},
		{"server.seo.twitter.site", "server.seo.twitter_handle"},
		{"server.branding.logo_url", "server.branding.logo"},
		{"server.branding.favicon_url", "server.branding.favicon"},
		{"web.favicon_url", "server.branding.favicon"},
		{"web.ui.favicon", "server.branding.favicon"},
		{"web.ui.logo", "server.branding.logo"},
		{"server.admin.email", "server.contact.admin.email"},
		{"server.ssl.dns01", "server.ssl.letsencrypt.dns_credentials"},
		{"server.ssl.letsencrypt.provider", "server.ssl.letsencrypt.dns_provider"},
		{"server.ssl.letsencrypt.credentials_encrypted", "server.ssl.letsencrypt.dns_credentials"},
		{"server.ssl.letsencrypt.validated_at", ""},
		{"server.ssl.letsencrypt.domain", ""},
		{"server.ssl.letsencrypt.domains", ""},
	} {
		if pair[1] == "" {
			_ = takeYAMLConfigPath(root, pair[0])
		} else {
			move(pair[0], pair[1])
		}
	}
	move("server.logs.access_format", "server.logs.access.format")
	database := mappingNodeValueAtPath(root, "server.database")
	if database != nil && database.Kind == yaml.MappingNode {
		if legacyType := mappingNodeValue(database, "type"); legacyType != nil && mappingNodeValue(database, "driver") == nil {
			setYAMLConfigKey(database, "driver", cloneYAMLConfigNode(legacyType))
			removeYAMLConfigKey(database, "type")
		}
		if primary := mappingNodeValue(database, "primary"); primary != nil && primary.Kind == yaml.MappingNode {
			mergeMissingYAMLConfig(database, primary)
			removeYAMLConfigKey(database, "primary")
		}
	}
	if value := takeYAMLConfigPath(root, "server.web"); value != nil {
		web := mappingNodeValue(root, "web")
		if web == nil || web.Kind != yaml.MappingNode {
			web = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setYAMLConfigKey(root, "web", web)
		}
		mergeMissingYAMLConfig(web, value)
	}
	move("web.csrf", "server.csrf")
	if value := takeYAMLConfigPath(root, "server.https_port"); value != nil {
		port := mappingNodeValueAtPath(root, "server.port")
		if port != nil && port.Kind == yaml.ScalarNode && !strings.Contains(port.Value, ",") {
			if extra, err := strconv.Atoi(strings.TrimSpace(value.Value)); err == nil && extra > 0 {
				port.Value += "," + strconv.Itoa(extra)
				port.Tag = "!!str"
			}
		}
	}
	if legacy := takeYAMLConfigPath(root, "server.seo.default_title"); legacy != nil && (mappingNodeValueAtPath(root, "server.branding.title") == nil || mappingNodeValueAtPath(root, "server.branding.title").Value == "") {
		setYAMLConfigPath(root, "server.branding.title", legacy)
	}
	if legacy := takeYAMLConfigPath(root, "server.seo.default_description"); legacy != nil && (mappingNodeValueAtPath(root, "server.branding.description") == nil || mappingNodeValueAtPath(root, "server.branding.description").Value == "") {
		setYAMLConfigPath(root, "server.branding.description", legacy)
	}
	if legacy := takeYAMLConfigPath(root, "server.branding.footer_text"); legacy != nil {
		content := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setYAMLConfigKey(content, "format", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "html"})
		setYAMLConfigKey(content, "body", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "<p>" + html.EscapeString(legacy.Value) + "</p>"})
		setYAMLConfigPath(root, "server.branding.content.footer", content)
	}
	if legacy := takeYAMLConfigPath(root, "web.footer.custom_html"); legacy != nil && mappingNodeValueAtPath(root, "server.branding.content.footer") == nil {
		setYAMLConfigPath(root, "server.branding.content.footer.format", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "html"})
		setYAMLConfigPath(root, "server.branding.content.footer.body", legacy)
	}
	for _, page := range []string{"about", "privacy", "terms"} {
		if legacy := takeYAMLConfigPath(root, "server.pages."+page+".content"); legacy != nil {
			setYAMLConfigPath(root, "server.branding.content."+page+".format", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "md"})
			setYAMLConfigPath(root, "server.branding.content."+page+".body", legacy)
		}
	}
	if tasks := mappingNodeValueAtPath(root, "server.scheduler.tasks"); tasks != nil && tasks.Kind == yaml.MappingNode {
		defaultData, _ := yaml.Marshal(defaultSchedulerConfig().Tasks)
		var defaultDocument yaml.Node
		_ = yaml.Unmarshal(defaultData, &defaultDocument)
		var defaultTasks *yaml.Node
		if len(defaultDocument.Content) > 0 {
			defaultTasks = defaultDocument.Content[0]
		}
		for index := 0; index+1 < len(tasks.Content); index += 2 {
			taskName := tasks.Content[index].Value
			task := tasks.Content[index+1]
			if task.Kind != yaml.MappingNode {
				continue
			}
			allowed := map[string]bool{"enabled": true, "schedule": true, "retry_on_fail": true, "retry_delay": true}
			if tasks.Content[index].Value == "backup" {
				allowed["retention"] = true
			}
			for fieldIndex := 0; fieldIndex+1 < len(task.Content); {
				if !allowed[task.Content[fieldIndex].Value] {
					task.Content = append(task.Content[:fieldIndex], task.Content[fieldIndex+2:]...)
					continue
				}
				fieldIndex += 2
			}
			if defaultTask := mappingNodeValue(defaultTasks, taskName); defaultTask != nil && yamlNodesEqual(task, defaultTask) {
				tasks.Content = append(tasks.Content[:index], tasks.Content[index+2:]...)
				index -= 2
			}
		}
	}
	for _, key := range []string{"installation_secret", "installation_secret_previous", "installation_secret_previous_expires", "encryption_key_previous", "encryption_key_previous_expires"} {
		path := "server.security." + key
		if value := takeYAMLConfigPath(root, path); value != nil && value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
			legacyAppSecrets.Lock()
			legacyAppSecrets.values[key] = value.Value
			legacyAppSecrets.Unlock()
		}
	}
	for _, path := range []string{
		"server.web.cookie_consent.enabled", "server.security_headers", "server.security.headers", "server.security.installation_secret",
		"server.security.installation_secret_previous", "server.security.installation_secret_previous_expires",
		"server.security.encryption_key_previous", "server.security.encryption_key_previous_expires", "server.seo.enabled",
		"server.seo.opengraph.description", "server.seo.sitemap.enabled", "server.seo.feed.enabled",
		"server.branding.theme_color", "server.branding.source_code_url", "server.admin.path", "server.admin.password", "server.admin.token",
		"server.admin.username", "server.admin.two_factor", "web.llms.enabled", "server.privacy.content", "server.mcp",
		"server.rate_limit.enabled", "server.rate_limit.requests", "server.rate_limit.window", "server.scheduler.enabled", "server.maintenance_mode",
		"server.i18n.auto_detect", "server.i18n.rtl_languages", "server.i18n.show_selector", "server.i18n.translations_dir",
		"server.geoip.dir", "server.geoip.databases", "server.geoip.country_mode", "server.metrics.endpoint",
		"server.notifications.email_enabled", "server.notifications.webhook_enabled", "server.secret_key", "web.permissions_policy", "web.honor_dnt", "web.robots.ai_bots",
	} {
		remove(path)
	}
}

func yamlNodesEqual(left, right *yaml.Node) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Kind != right.Kind || left.Tag != right.Tag || left.Value != right.Value || len(left.Content) != len(right.Content) {
		return false
	}
	if left.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(left.Content); index += 2 {
			match := false
			for other := 0; other+1 < len(right.Content); other += 2 {
				if yamlNodesEqual(left.Content[index], right.Content[other]) {
					if !yamlNodesEqual(left.Content[index+1], right.Content[other+1]) {
						return false
					}
					match = true
					break
				}
			}
			if !match {
				return false
			}
		}
		return true
	}
	for index := range left.Content {
		if !yamlNodesEqual(left.Content[index], right.Content[index]) {
			return false
		}
	}
	return true
}

func migrateSecretsFromDropIns(directory string, candidates *yaml.Node) (map[string]*yaml.Node, bool) {
	changed := map[string]*yaml.Node{}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return changed, false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	any := false
	for _, entry := range entries {
		if !isYAMLConfigDropInName(entry.Name()) {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		resolved, eligible, err := resolveYAMLConfigDropIn(directory, path)
		if err != nil || !eligible {
			continue
		}
		document := readExistingYAMLMappingAt(resolved)
		if document == nil {
			continue
		}
		before, _ := os.ReadFile(resolved)
		applyLegacyConfigMigrations(document)
		secretChanged := extractConfigSecrets(document, candidates)
		after, marshalErr := yaml.Marshal(document)
		if secretChanged || marshalErr == nil && !bytes.Equal(before, after) {
			changed[resolved] = document
			any = true
		}
	}
	return changed, any
}

func readExistingYAMLMappingAt(path string) *yaml.Node {
	var document yaml.Node
	data, err := os.ReadFile(path)
	if err != nil || yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	return document.Content[0]
}

func extractConfigSecrets(source, candidates *yaml.Node) bool {
	changed := false
	for _, dotted := range []string{
		"server.token", "server.security.encryption_key", "server.security.encryption_key_version",
		"server.notifications.email.smtp.password", "server.cache.password", "server.database.token",
		"server.pages.contact.captcha_secret_key", "server.ssl.letsencrypt.dns_credentials", "data.cve.api_key",
		"server.backup.encryption_password", "server.database.url", "server.cache.url",
	} {
		value := mappingNodeValueAtPath(source, dotted)
		if value == nil {
			continue
		}
		if strings.HasSuffix(dotted, ".url") && !urlHasCredential(value.Value) {
			continue
		}
		value = takeYAMLConfigPath(source, dotted)
		setYAMLConfigPath(candidates, dotted, value)
		changed = true
	}
	if tokens := mappingNodeValueAtPath(source, "server.metrics.auth.tokens"); tokens != nil && tokens.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(tokens.Content); {
			name := tokens.Content[index].Value
			dotted := "server.metrics.auth.tokens." + name
			if value := takeYAMLConfigPath(source, dotted); value != nil {
				setYAMLConfigPath(candidates, dotted, value)
				changed = true
				continue
			}
			index += 2
		}
	}
	webhooks := mappingNodeValueAtPath(source, "server.contact")
	if webhooks != nil && webhooks.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(webhooks.Content); index += 2 {
			role := webhooks.Content[index].Value
			if hooks := mappingNodeValue(webhooks.Content[index+1], "webhooks"); hooks != nil && hooks.Kind == yaml.MappingNode {
				for hookIndex := 0; hookIndex+1 < len(hooks.Content); {
					name := hooks.Content[hookIndex].Value
					value := hooks.Content[hookIndex+1]
					if value.Kind == yaml.ScalarNode {
						dotted := "server.contact." + role + ".webhooks." + name
						_ = takeYAMLConfigPath(source, dotted)
						setYAMLConfigPath(candidates, dotted, cloneYAMLConfigNode(value))
						changed = true
						continue
					}
					hookIndex += 2
				}
			}
		}
	}
	return changed
}

func urlHasCredential(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		_, password := parsed.User.Password()
		if password || parsed.User.Username() != "" {
			return true
		}
	}
	query := parsed.Query()
	for _, key := range []string{"password", "pwd", "token", "authtoken"} {
		if query.Get(key) != "" {
			return true
		}
	}
	return false
}

func mergeIntoExistingDropIn(path string, migrated *yaml.Node) error {
	merged := cloneYAMLConfigNode(migrated)
	if existing, err := os.ReadFile(path); err == nil {
		var document yaml.Node
		if yaml.Unmarshal(existing, &document) == nil && len(document.Content) == 1 && document.Content[0].Kind == yaml.MappingNode {
			merged = cloneYAMLConfigNode(document.Content[0])
			mergeMissingYAMLConfig(merged, migrated)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	return writeAtomicPrivateFile(path, data)
}

func mergeMissingYAMLConfig(destination, source *yaml.Node) {
	if destination == nil || source == nil || destination.Kind != yaml.MappingNode || source.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index+1 < len(source.Content); index += 2 {
		key := source.Content[index].Value
		value := source.Content[index+1]
		current := mappingNodeValue(destination, key)
		if current == nil {
			setYAMLConfigKey(destination, key, cloneYAMLConfigNode(value))
		} else if current.Kind == yaml.MappingNode && value.Kind == yaml.MappingNode {
			mergeMissingYAMLConfig(current, value)
		}
	}
}

func setYAMLConfigPath(root *yaml.Node, dottedPath string, value *yaml.Node) {
	parts := bytes.Split([]byte(dottedPath), []byte("."))
	current := root
	for _, part := range parts[:len(parts)-1] {
		key := string(part)
		next := mappingNodeValue(current, key)
		if next == nil || next.Kind != yaml.MappingNode {
			next = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			setYAMLConfigKey(current, key, next)
		}
		current = next
	}
	setYAMLConfigKey(current, string(parts[len(parts)-1]), value)
}

func readExistingYAMLMapping(path string) (*yaml.Node, error) {
	var document yaml.Node
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("secrets.yml must be a YAML mapping")
	}
	return document.Content[0], nil
}

func areaNodesInMigrationOrder(areas map[string]*yaml.Node) []*yaml.Node {
	ordered := []string{"branding", "web", "pages", "contact", "privacy", "security", "logs", "metrics", "notifications", "tor", "i2p", "maintenance", "app"}
	nodes := make([]*yaml.Node, 0, len(areas))
	for _, name := range ordered {
		if areas[name] != nil {
			nodes = append(nodes, areas[name])
		}
	}
	return nodes
}

func mappingNodeValueAtPath(root *yaml.Node, dottedPath string) *yaml.Node {
	current := root
	parts := bytes.Split([]byte(dottedPath), []byte("."))
	for _, part := range parts {
		current = mappingNodeValue(current, string(part))
		if current == nil {
			return nil
		}
	}
	return current
}

func takeYAMLConfigPath(root *yaml.Node, dottedPath string) *yaml.Node {
	parts := bytes.Split([]byte(dottedPath), []byte("."))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = mappingNodeValue(current, string(part))
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
	}
	key := string(parts[len(parts)-1])
	value := mappingNodeValue(current, key)
	if value != nil {
		removeYAMLConfigKey(current, key)
	}
	return value
}
