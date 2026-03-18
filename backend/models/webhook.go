package models

// WebhookPayload is the top-level structure Meta sends to your webhook endpoint.
type WebhookPayload struct {
	Object string  `json:"object"`
	Entry  []Entry `json:"entry"`
}

type Entry struct {
	ID      string   `json:"id"`
	Changes []Change `json:"changes"`
}

type Change struct {
	Value ChangeValue `json:"value"`
	Field string      `json:"field"`
}

type ChangeValue struct {
	MessagingProduct string    `json:"messaging_product"`
	Metadata         Metadata  `json:"metadata"`
	Contacts         []Contact `json:"contacts,omitempty"`
	Messages         []Message `json:"messages,omitempty"`
	Statuses         []Status  `json:"statuses,omitempty"`
}

type Metadata struct {
	DisplayPhoneNumber string `json:"display_phone_number"`
	PhoneNumberID      string `json:"phone_number_id"`
}

type Contact struct {
	Profile Profile `json:"profile"`
	WaID    string  `json:"wa_id"`
}

type Profile struct {
	Name string `json:"name"`
}

type Message struct {
	From      string     `json:"from"`
	ID        string     `json:"id"`
	Timestamp string     `json:"timestamp"`
	Type      string     `json:"type"`
	Image     *MediaInfo `json:"image,omitempty"`
	Video     *MediaInfo `json:"video,omitempty"`
	Audio     *MediaInfo `json:"audio,omitempty"`
	Document  *MediaInfo `json:"document,omitempty"`
	Sticker   *MediaInfo `json:"sticker,omitempty"`
}

// MediaInfo contains the media_id (and optional caption/filename) for any media message.
type MediaInfo struct {
	ID       string `json:"id"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename,omitempty"` // documents only
	Caption  string `json:"caption,omitempty"`
}

// MediaURLResponse is returned by GET /<media_id> from the WhatsApp API.
type MediaURLResponse struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	SHA256   string `json:"sha256"`
	FileSize int64  `json:"file_size"`
	ID       string `json:"id"`
}

type Status struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Timestamp    string `json:"timestamp"`
	RecipientID  string `json:"recipient_id"`
}
