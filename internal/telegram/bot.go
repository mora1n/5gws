package telegram

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/morain/5gws/internal/config"
)

type Bot struct {
	token   string
	allowed map[int64]bool
	client  http.Client
	handler handler
}

type update struct {
	UpdateID      int64            `json:"update_id"`
	Message       *telegramMessage `json:"message"`
	CallbackQuery *callbackQuery   `json:"callback_query"`
}

type telegramMessage struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      struct {
		ID int64 `json:"id"`
	} `json:"chat"`
	From struct {
		ID int64 `json:"id"`
	} `json:"from"`
}

type callbackQuery struct {
	ID      string           `json:"id"`
	Data    string           `json:"data"`
	From    telegramUser     `json:"from"`
	Message *telegramMessage `json:"message"`
}

type telegramUser struct {
	ID int64 `json:"id"`
}

type updateResponse struct {
	OK     bool     `json:"ok"`
	Result []update `json:"result"`
}

type inlineKeyboard struct {
	InlineKeyboard [][]inlineButton `json:"inline_keyboard"`
}

type inlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type botResponse struct {
	Text   string
	Markup *inlineKeyboard
}

func Run(ctx context.Context, cfg config.Config, cfgPath, rulesPath string) error {
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
		handler: newHandler(cfgPath, rulesPath),
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
			log.Printf("telegram getUpdates: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, update := range updates.Result {
			offset = update.UpdateID + 1
			switch {
			case update.Message != nil:
				b.handleMessage(*update.Message)
			case update.CallbackQuery != nil:
				b.handleCallback(*update.CallbackQuery)
			}
		}
	}
}

func (b Bot) handleMessage(message telegramMessage) {
	if !b.isAllowed(message.From.ID) {
		if err := b.send(message.Chat.ID, "forbidden", nil); err != nil {
			log.Printf("telegram send forbidden: %v", err)
		}
		return
	}
	resp := b.handler.handleText(message.Text)
	if err := b.send(message.Chat.ID, resp.Text, resp.Markup); err != nil {
		log.Printf("telegram send: %v", err)
	}
}

func (b Bot) handleCallback(query callbackQuery) {
	if !b.isAllowed(query.From.ID) {
		if err := b.answerCallback(query.ID, "forbidden"); err != nil {
			log.Printf("telegram answer forbidden callback: %v", err)
		}
		return
	}
	if err := b.answerCallback(query.ID, ""); err != nil {
		log.Printf("telegram answer callback: %v", err)
	}
	if query.Message == nil {
		log.Printf("telegram callback %q has no message", query.ID)
		return
	}
	if callbackNeedsProgress(query.Data) {
		if err := b.edit(query.Message.Chat.ID, query.Message.MessageID, "执行中...", nil); err != nil {
			log.Printf("telegram edit progress: %v", err)
		}
	}
	resp := b.handler.handleCallback(query.Data)
	if err := b.edit(query.Message.Chat.ID, query.Message.MessageID, resp.Text, resp.Markup); err != nil {
		log.Printf("telegram edit: %v", err)
		if err := b.send(query.Message.Chat.ID, resp.Text, resp.Markup); err != nil {
			log.Printf("telegram send after edit failed: %v", err)
		}
	}
}

func (b Bot) isAllowed(userID int64) bool {
	return len(b.allowed) == 0 || b.allowed[userID]
}

func callbackNeedsProgress(data string) bool {
	switch data {
	case "cmd:doctor", "confirm:apply", "confirm:restart":
		return true
	default:
		return false
	}
}

func (b Bot) send(chatID int64, text string, markup *inlineKeyboard) error {
	values := url.Values{}
	values.Set("chat_id", strconv.FormatInt(chatID, 10))
	values.Set("text", truncateText(text))
	values.Set("disable_web_page_preview", "true")
	if err := setMarkup(values, markup); err != nil {
		return err
	}
	return b.postForm("sendMessage", values)
}

func (b Bot) edit(chatID, messageID int64, text string, markup *inlineKeyboard) error {
	values := url.Values{}
	values.Set("chat_id", strconv.FormatInt(chatID, 10))
	values.Set("message_id", strconv.FormatInt(messageID, 10))
	values.Set("text", truncateText(text))
	values.Set("disable_web_page_preview", "true")
	if err := setMarkup(values, markup); err != nil {
		return err
	}
	return b.postForm("editMessageText", values)
}

func (b Bot) answerCallback(callbackID, text string) error {
	values := url.Values{}
	values.Set("callback_query_id", callbackID)
	if text != "" {
		values.Set("text", text)
	}
	return b.postForm("answerCallbackQuery", values)
}

func setMarkup(values url.Values, markup *inlineKeyboard) error {
	if markup == nil {
		return nil
	}
	data, err := json.Marshal(markup)
	if err != nil {
		return err
	}
	values.Set("reply_markup", string(data))
	return nil
}

func (b Bot) postForm(method string, values url.Values) error {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.token, method)
	resp, err := b.client.PostForm(endpoint, values)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s failed: %s", method, resp.Status)
	}
	return nil
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
