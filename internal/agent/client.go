/**
 * [INPUT]: 依赖 bufio、bytes、context、crypto/rand、encoding/json、fmt、io、net/http、strings、time
 * [OUTPUT]: 对外提供 Client（gateway /v1/chat/completions 的流式调用）、Message、NewSessionID、APIError
 * [POS]: internal/agent 的传输层——keyless 聊天：OpenAI 兼容 SSE 指向 gateway，
 *        设备端零厂商 key（agent-design/docs/execution.md §8.2）；模型名是平台别名
 * [PROTOCOL]: 变更时更新此头部，然后检查 CLAUDE.md
 */

package agent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message 是 OpenAI 兼容的对话消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// APIError 还原 gateway / llm-service 的 OpenAI 风格错误体。
type APIError struct {
	HTTPStatus int
	Type       string
	Msg        string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("llm %d %s: %s", e.HTTPStatus, e.Type, e.Msg)
}

// Client 是 keyless 聊天通道：只持平台 token，指向 gateway 的 LLM 代理面。
type Client struct {
	gatewayURL string
	token      string
	sessionID  string
	http       *http.Client
}

// NewClient 构造 Client。sessionID 进 X-Session-ID 头，做服务端计量的会话维度。
func NewClient(gatewayURL, token, sessionID string) *Client {
	return &Client{
		gatewayURL: strings.TrimSuffix(gatewayURL, "/"),
		token:      token,
		sessionID:  sessionID,
		// 流式响应无总超时；连接与首字节交给服务端与 ctx 兜底。
		http: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 120 * time.Second}},
	}
}

// NewSessionID 生成本地会话标识（计量维度，无安全语义）。
func NewSessionID() string {
	buffer := make([]byte, 8)
	_, _ = rand.Read(buffer)
	return fmt.Sprintf("session_local_%x", buffer)
}

// chatRequest 是发往 /v1/chat/completions 的最小请求体；model 填平台别名。
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

// streamChunk 是 SSE data 载荷里本层关心的最小形状。
type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

// ChatStream 发起流式补全，每个内容增量回调 onDelta，返回完整回复文本。
func (c *Client) ChatStream(ctx context.Context, model string, messages []Message, onDelta func(string)) (string, error) {
	bodyJSON, err := json.Marshal(chatRequest{Model: model, Messages: messages, Stream: true})
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.gatewayURL+"/v1/chat/completions", bytes.NewReader(bodyJSON))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("X-Session-ID", c.sessionID)

	response, err := c.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("gateway unreachable: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", decodeAPIError(response)
	}

	var full strings.Builder
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:"); ok {
			payload = strings.TrimSpace(payload)
			if payload == "[DONE]" {
				break
			}
			var chunk streamChunk
			if json.Unmarshal([]byte(payload), &chunk) == nil && len(chunk.Choices) > 0 {
				if delta := chunk.Choices[0].Delta.Content; delta != "" {
					full.WriteString(delta)
					if onDelta != nil {
						onDelta(delta)
					}
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return full.String(), fmt.Errorf("read stream: %w", err)
		}
	}
	return full.String(), nil
}

// decodeAPIError 解 OpenAI 风格错误体；解不出时保底带状态码与原文摘要。
func decodeAPIError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error.Message != "" {
		return &APIError{HTTPStatus: response.StatusCode, Type: payload.Error.Type, Msg: payload.Error.Message}
	}
	summary := strings.TrimSpace(string(body))
	if len(summary) > 200 {
		summary = summary[:200]
	}
	return &APIError{HTTPStatus: response.StatusCode, Type: "unknown", Msg: summary}
}
