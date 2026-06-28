package appserver

import "encoding/json"

type DynamicToolFunction struct {
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	DeferLoading bool            `json:"deferLoading,omitempty"`
}

type DynamicToolNamespace struct {
	Type        string                `json:"type"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Tools       []DynamicToolFunction `json:"tools"`
}

type DynamicToolCallParams struct {
	ThreadID  string          `json:"threadId"`
	TurnID    string          `json:"turnId"`
	CallID    string          `json:"callId"`
	Namespace string          `json:"namespace,omitempty"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type DynamicToolCallResponse struct {
	ContentItems []DynamicToolContentItem `json:"contentItems"`
	Success      bool                     `json:"success"`
}

type DynamicToolContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type threadResponse struct {
	Thread struct {
		ID string `json:"id"`
	} `json:"thread"`
}

type turnStartResponse struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

type turnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"turn"`
}

type errorNotificationParams struct {
	ThreadID  string `json:"threadId"`
	TurnID    string `json:"turnId"`
	WillRetry bool   `json:"willRetry"`
	Error     struct {
		Message string `json:"message"`
	} `json:"error"`
}
