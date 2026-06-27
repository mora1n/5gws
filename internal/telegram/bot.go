package telegram

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/morain/5gws/internal/config"
)

type Bot struct {
	token   string
	allowed map[int64]bool
	client  http.Client
}

type updateResponse struct {
	OK     bool `json:"ok"`
	Result []struct {
		UpdateID int64 `json:"update_id"`
		Message  struct {
			Text string `json:"text"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			From struct {
				ID int64 `json:"id"`
			} `json:"from"`
		} `json:"message"`
	} `json:"result"`
}

func Run(ctx context.Context, cfg config.Config) error {
	if !cfg.Telegram.Enabled {
		return errors.New("telegram.enabled=false")
	}
	env, err := readEnv(cfg.Telegram.BotEnv)
	if err != nil {
		return err
	}
	token := env["BOT_TOKEN"]
	if token == "" {
		token = env["TELEGRAM_BOT_TOKEN"]
	}
	if token == "" {
		return errors.New("BOT_TOKEN is required in bot.env")
	}
	bot := Bot{
		token:   token,
		allowed: allowedUsers(cfg.Telegram.AllowedUsers, env["TELEGRAM_ALLOWED_USERS"]),
		client:  http.Client{Timeout: 30 * time.Second},
	}
	return bot.loop(ctx)
}

func (b Bot) loop(ctx context.Context) error {
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updates, err := b.getUpdates(offset)
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		for _, update := range updates.Result {
			offset = update.UpdateID + 1
			chatID := update.Message.Chat.ID
			userID := update.Message.From.ID
			if len(b.allowed) > 0 && !b.allowed[userID] {
				_ = b.send(chatID, "forbidden")
				continue
			}
			_ = b.send(chatID, handle(update.Message.Text))
		}
	}
}

func (b Bot) getUpdates(offset int64) (updateResponse, error) {
	values := url.Values{}
	values.Set("timeout", "25")
	if offset > 0 {
		values.Set("offset", strconv.FormatInt(offset, 10))
	}
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?%s", b.token, values.Encode())
	resp, err := b.client.Get(endpoint)
	if err != nil {
		return updateResponse{}, err
	}
	defer resp.Body.Close()
	var updates updateResponse
	if err := json.NewDecoder(resp.Body).Decode(&updates); err != nil {
		return updateResponse{}, err
	}
	if !updates.OK {
		return updateResponse{}, errors.New("telegram getUpdates returned ok=false")
	}
	return updates, nil
}

func (b Bot) send(chatID int64, text string) error {
	values := url.Values{}
	values.Set("chat_id", strconv.FormatInt(chatID, 10))
	values.Set("text", text)
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", b.token)
	resp, err := b.client.PostForm(endpoint, values)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram sendMessage failed: %s", resp.Status)
	}
	return nil
}

func handle(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "empty command; send /help"
	}
	switch fields[0] {
	case "/start", "/help":
		return "/status\n/doctor\n/ios"
	case "/status":
		return runCLI("status")
	case "/doctor":
		return runCLI("doctor")
	case "/ios":
		return runCLI("ios-link")
	default:
		return "unknown command; send /help"
	}
}

func runCLI(args ...string) string {
	cmd := exec.Command("/usr/local/bin/5gws", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(buf.String() + "\n" + err.Error())
	}
	return strings.TrimSpace(buf.String())
}

func readEnv(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	env := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid env line %q", line)
		}
		env[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return env, scanner.Err()
}

func allowedUsers(configured []string, envValue string) map[int64]bool {
	out := map[int64]bool{}
	values := append([]string{}, configured...)
	values = append(values, strings.Split(envValue, ",")...)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			out[id] = true
		}
	}
	return out
}
