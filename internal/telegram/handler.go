package telegram

import (
	"bytes"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

const (
	defaultBinary        = "/usr/local/bin/5gws"
	telegramMessageLimit = 3900
	defaultLogLines      = 60
	maxLogLines          = 200
)

type runnerFunc func(name string, args ...string) string
type checkedRunnerFunc func(name string, args ...string) (string, error)

type handler struct {
	binary        string
	configPath    string
	rulesPath     string
	runner        runnerFunc
	checkedRunner checkedRunnerFunc
	loadConfig    func() (config.Config, error)
	loadRules     func() (rules.File, error)
}

func newHandler(cfgPath, rulesPath string) handler {
	h := handler{
		binary:     defaultBinary,
		configPath: cfgPath,
		rulesPath:  rulesPath,
		runner:     runCommand,
	}
	h.checkedRunner = runCommandChecked
	h.loadConfig = func() (config.Config, error) {
		return config.Load(h.configPath)
	}
	h.loadRules = func() (rules.File, error) {
		return rules.Load(h.rulesPath)
	}
	return h
}

func (h handler) handleText(text string) botResponse {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return menuResponse()
	}
	command := normalizeCommand(fields[0])
	args := fields[1:]
	switch command {
	case "/start", "/help", "/menu":
		return menuResponse()
	case "/status":
		return outputResponse(h.run5gws("status"))
	case "/doctor":
		return outputResponse(h.run5gws("doctor", "--config", h.configPath, "--rules", h.rulesPath))
	case "/ios":
		return outputResponse(h.run5gws("ios-link", "--config", h.configPath, "--no-qr"))
	case "/config":
		return outputResponse(h.configSummary())
	case "/rules":
		return outputResponse(h.rulesSummary())
	case "/rule_list":
		return outputResponse(h.managedRulesSummary())
	case "/rule_add":
		return h.handleRuleAdd(args)
	case "/rule_del":
		return h.handleRuleDel(args)
	case "/logs":
		return outputResponse(h.logs(parseLogLines(args)))
	case "/apply":
		return confirmApplyResponse()
	case "/restart":
		return confirmRestartResponse()
	default:
		return botResponse{Text: "未知命令，发送 /help 打开菜单。", Markup: menuKeyboard()}
	}
}

func (h handler) handleCallback(data string) botResponse {
	switch data {
	case "menu", "cancel":
		return menuResponse()
	case "cmd:status":
		return h.handleText("/status")
	case "cmd:doctor":
		return h.handleText("/doctor")
	case "cmd:ios":
		return h.handleText("/ios")
	case "cmd:config":
		return h.handleText("/config")
	case "cmd:rules":
		return h.handleText("/rules")
	case "cmd:rule_list":
		return h.handleText("/rule_list")
	case "cmd:logs":
		return h.handleText("/logs")
	case "ask:apply":
		return confirmApplyResponse()
	case "ask:restart":
		return confirmRestartResponse()
	case "confirm:apply":
		return outputResponse(h.run5gws("apply", "--config", h.configPath, "--rules", h.rulesPath, "--skip-bot-restart"))
	case "confirm:restart":
		return outputResponse(h.restartServices())
	default:
		return botResponse{Text: "未知按钮，返回菜单。", Markup: menuKeyboard()}
	}
}

func (h handler) run5gws(args ...string) string {
	return h.runner(h.binary, args...)
}

func normalizeCommand(command string) string {
	if at := strings.Index(command, "@"); at > 0 {
		command = command[:at]
	}
	return command
}

func menuResponse() botResponse {
	return botResponse{Text: menuText(), Markup: menuKeyboard()}
}

func menuText() string {
	return `5gws Telegram 管理

/status  服务状态
/doctor  配置和运行依赖检查
/ios     iOS 描述文件链接
/config  配置摘要
/rules   规则摘要
/rule_list  Telegram 管理规则
/rule_add <domain> <exit|pool:name>  添加规则
/rule_del <name>  删除 Telegram 管理规则
/logs    运行日志
`
}

func menuKeyboard() *inlineKeyboard {
	return &inlineKeyboard{InlineKeyboard: [][]inlineButton{
		{{Text: "状态", CallbackData: "cmd:status"}, {Text: "自检", CallbackData: "cmd:doctor"}},
		{{Text: "iOS 链接", CallbackData: "cmd:ios"}, {Text: "配置", CallbackData: "cmd:config"}, {Text: "规则", CallbackData: "cmd:rules"}},
		{{Text: "规则编辑", CallbackData: "cmd:rule_list"}, {Text: "日志", CallbackData: "cmd:logs"}},
	}}
}

func backKeyboard() *inlineKeyboard {
	return &inlineKeyboard{InlineKeyboard: [][]inlineButton{
		{{Text: "返回菜单", CallbackData: "menu"}},
	}}
}

func outputResponse(text string) botResponse {
	text = strings.TrimSpace(text)
	if text == "" {
		text = "(no output)"
	}
	return botResponse{Text: truncateText(text), Markup: backKeyboard()}
}

func confirmApplyResponse() botResponse {
	return botResponse{
		Text: "确认执行 5gws apply？\n会重新渲染配置并重启 5gws 运行服务；本次不会重启 bot 自身。",
		Markup: &inlineKeyboard{InlineKeyboard: [][]inlineButton{
			{{Text: "确认应用", CallbackData: "confirm:apply"}, {Text: "取消", CallbackData: "cancel"}},
		}},
	}
}

func confirmRestartResponse() botResponse {
	return botResponse{
		Text: "确认重启 5gws 运行服务？\nbot 自身不会被重启。",
		Markup: &inlineKeyboard{InlineKeyboard: [][]inlineButton{
			{{Text: "确认重启", CallbackData: "confirm:restart"}, {Text: "取消", CallbackData: "cancel"}},
		}},
	}
}

func parseLogLines(args []string) int {
	if len(args) == 0 {
		return defaultLogLines
	}
	lines, err := strconv.Atoi(args[0])
	if err != nil || lines <= 0 {
		return defaultLogLines
	}
	if lines > maxLogLines {
		return maxLogLines
	}
	return lines
}

func truncateText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "(no output)"
	}
	if len(text) <= telegramMessageLimit {
		return text
	}
	suffix := "\n\n[output truncated]"
	cut := telegramMessageLimit - len(suffix)
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	if cut <= 0 {
		return suffix
	}
	return text[:cut] + suffix
}

func runCommand(name string, args ...string) string {
	out, err := runCommandChecked(name, args...)
	if err != nil {
		if out == "" {
			return err.Error()
		}
		return out + "\n" + err.Error()
	}
	return out
}

func runCommandChecked(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(buf.String())
		return out, err
	}
	return strings.TrimSpace(buf.String()), nil
}
