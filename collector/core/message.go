package core

// Message is a single collected record: a structured payload plus metadata
// describing where it came from.
type Message struct {
	Data map[string]any    `json:"data"`
	Meta map[string]string `json:"meta"`
}

// NewMessage returns a Message with an initialised metadata map.
func NewMessage(data map[string]any) Message {
	return Message{Data: data, Meta: map[string]string{}}
}
