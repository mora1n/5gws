package telegram

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/morain/5gws/internal/rules"
	"github.com/pelletier/go-toml/v2"
)

const (
	managedRulesBegin = "# BEGIN 5gws telegram managed rules"
	managedRulesEnd   = "# END 5gws telegram managed rules"
)

func (h handler) handleRuleAdd(args []string) botResponse {
	if len(args) != 2 {
		return outputResponse("usage: /rule_add <domain> <exit|pool:name>")
	}
	text, ok := h.addManagedRule(args[0], args[1])
	if !ok {
		return outputResponse(text)
	}
	return botResponse{Text: truncateText(text + "\n\n确认应用后生效。"), Markup: applyNowKeyboard()}
}

func (h handler) handleRuleDel(args []string) botResponse {
	if len(args) != 1 {
		return outputResponse("usage: /rule_del <rule_name>")
	}
	text, ok := h.deleteManagedRule(args[0])
	if !ok {
		return outputResponse(text)
	}
	return botResponse{Text: truncateText(text + "\n\n确认应用后生效。"), Markup: applyNowKeyboard()}
}

func (h handler) managedRulesSummary() string {
	data, err := os.ReadFile(h.rulesPath)
	if err != nil {
		return "load rules: " + err.Error()
	}
	managed, err := parseManagedRules(string(data))
	if err != nil {
		return "load managed rules: " + err.Error()
	}
	if len(managed) == 0 {
		return "Telegram managed rules: 0\nadd: /rule_add <domain> <exit|pool:name>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Telegram managed rules: %d\n", len(managed))
	for _, rule := range managed {
		fmt.Fprintf(&b, "- %s target=%s domains=%s\n", rule.Name, targetName(rule.Exit, rule.DNSPool), strings.Join(rule.DomainSuffix, ","))
	}
	fmt.Fprintln(&b, "delete: /rule_del <rule_name>")
	return strings.TrimSpace(b.String())
}

func (h handler) addManagedRule(domain, target string) (string, bool) {
	rule, err := h.buildManagedRule(domain, target)
	if err != nil {
		return err.Error(), false
	}
	return h.updateManagedRules(func(managed []rules.Rule) ([]rules.Rule, string, error) {
		for _, existing := range managed {
			if existing.Name == rule.Name {
				return nil, "", fmt.Errorf("rule %q already exists", rule.Name)
			}
		}
		managed = append(managed, rule)
		return managed, "added rule " + rule.Name, nil
	})
}

func (h handler) deleteManagedRule(name string) (string, bool) {
	return h.updateManagedRules(func(managed []rules.Rule) ([]rules.Rule, string, error) {
		out := make([]rules.Rule, 0, len(managed))
		found := false
		for _, rule := range managed {
			if rule.Name == name {
				found = true
				continue
			}
			out = append(out, rule)
		}
		if !found {
			return nil, "", fmt.Errorf("managed rule %q not found", name)
		}
		return out, "deleted rule " + name, nil
	})
}

func (h handler) buildManagedRule(domain, target string) (rules.Rule, error) {
	domain, err := normalizeTelegramDomain(domain)
	if err != nil {
		return rules.Rule{}, err
	}
	rule := rules.Rule{
		Name:         managedRuleName(domain, target),
		DomainSuffix: []string{domain},
	}
	if pool, ok := strings.CutPrefix(target, "pool:"); ok {
		switch pool {
		case "cn", "overseas_private", "overseas_public":
			rule.DNSPool = pool
		default:
			return rules.Rule{}, fmt.Errorf("unknown dns pool %q", pool)
		}
	} else {
		cfg, err := h.loadConfig()
		if err != nil {
			return rules.Rule{}, fmt.Errorf("load config: %w", err)
		}
		if _, ok := cfg.ExitByName(target); !ok {
			return rules.Rule{}, fmt.Errorf("unknown exit %q", target)
		}
		rule.Exit = target
	}
	if _, err := rules.Normalize(rules.File{Rules: []rules.Rule{rule}}); err != nil {
		return rules.Rule{}, err
	}
	return rule, nil
}

func (h handler) updateManagedRules(modify func([]rules.Rule) ([]rules.Rule, string, error)) (string, bool) {
	data, err := os.ReadFile(h.rulesPath)
	if err != nil {
		return "load rules: " + err.Error(), false
	}
	managed, err := parseManagedRules(string(data))
	if err != nil {
		return "load managed rules: " + err.Error(), false
	}
	next, message, err := modify(managed)
	if err != nil {
		return err.Error(), false
	}
	if _, err := rules.Normalize(rules.File{Rules: next}); err != nil {
		return "validate managed rules: " + err.Error(), false
	}
	backup, err := writeRulesAtomically(h.rulesPath, data, []byte(replaceManagedBlock(string(data), next)))
	if err != nil {
		return "write rules: " + err.Error(), false
	}
	out, err := h.checkedRunner(h.binary, "doctor", "--config", h.configPath, "--rules", h.rulesPath)
	if err != nil {
		if restoreErr := restoreFile(backup, h.rulesPath); restoreErr != nil {
			return fmt.Sprintf("doctor failed and restore failed: %v\n%s\nrestore: %v", err, out, restoreErr), false
		}
		return fmt.Sprintf("doctor failed; restored %s\n%s\n%v", backup, out, err), false
	}
	return message + "\nbackup: " + backup + "\ndoctor: ok", true
}

func parseManagedRules(text string) ([]rules.Rule, error) {
	block, ok, err := managedBlock(text)
	if err != nil || !ok {
		return nil, err
	}
	var file rules.File
	if err := toml.Unmarshal([]byte(block), &file); err != nil {
		return nil, err
	}
	return file.Rules, nil
}

func managedBlock(text string) (string, bool, error) {
	begin := strings.Index(text, managedRulesBegin)
	if begin < 0 {
		return "", false, nil
	}
	end := strings.Index(text[begin:], managedRulesEnd)
	if end < 0 {
		return "", false, errors.New("managed block end marker missing")
	}
	start := begin + len(managedRulesBegin)
	stop := begin + end
	return strings.TrimSpace(text[start:stop]), true, nil
}

func replaceManagedBlock(text string, managed []rules.Rule) string {
	block := renderManagedRules(managed)
	begin := strings.Index(text, managedRulesBegin)
	if begin < 0 {
		if block == "" {
			return text
		}
		return strings.TrimRight(text, "\n") + "\n\n" + block + "\n"
	}
	end := strings.Index(text[begin:], managedRulesEnd)
	if end < 0 {
		return text
	}
	stop := begin + end + len(managedRulesEnd)
	if block == "" {
		return strings.TrimRight(text[:begin], "\n") + "\n" + strings.TrimLeft(text[stop:], "\n")
	}
	return text[:begin] + block + text[stop:]
}

func renderManagedRules(managed []rules.Rule) string {
	if len(managed) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintln(&b, managedRulesBegin)
	for _, rule := range managed {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "[[rules]]")
		fmt.Fprintf(&b, "name = %q\n", rule.Name)
		if rule.Exit != "" {
			fmt.Fprintf(&b, "exit = %q\n", rule.Exit)
		}
		if rule.DNSPool != "" {
			fmt.Fprintf(&b, "dns_pool = %q\n", rule.DNSPool)
		}
		fmt.Fprintf(&b, "domain_suffix = [%q]\n", rule.DomainSuffix[0])
	}
	fmt.Fprintln(&b)
	fmt.Fprint(&b, managedRulesEnd)
	return b.String()
}

func writeRulesAtomically(path string, current, next []byte) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	backup := fmt.Sprintf("%s.botbak-%s", path, time.Now().Format("20060102-150405.000000000"))
	if err := os.WriteFile(backup, current, info.Mode()); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(next); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Chmod(tmpName, info.Mode()); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return backup, nil
}

func restoreFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode())
}

func normalizeTelegramDomain(value string) (string, error) {
	domain := strings.ToLower(strings.Trim(strings.TrimSpace(value), "."))
	if domain == "" || net.ParseIP(domain) != nil || !strings.Contains(domain, ".") {
		return "", fmt.Errorf("invalid domain %q", value)
	}
	labelRE := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	for _, label := range strings.Split(domain, ".") {
		if !labelRE.MatchString(label) {
			return "", fmt.Errorf("invalid domain %q", value)
		}
	}
	return domain, nil
}

func managedRuleName(domain, target string) string {
	name := "tg_" + sanitizeRuleName(domain) + "_" + sanitizeRuleName(target)
	if len(name) > 80 {
		return name[:80]
	}
	return name
}

func sanitizeRuleName(value string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9]+`)
	return strings.Trim(re.ReplaceAllString(value, "_"), "_")
}

func applyNowKeyboard() *inlineKeyboard {
	return &inlineKeyboard{InlineKeyboard: [][]inlineButton{
		{{Text: "确认应用", CallbackData: "confirm:apply"}, {Text: "返回菜单", CallbackData: "menu"}},
	}}
}
