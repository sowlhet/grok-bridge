// Package translate converts Claude / OpenAI chat / OpenAI Responses payloads
// to and from xAI Responses format used by the Grok upstream.
package translate

// Format identifies an inbound or upstream protocol schema.
type Format string

const (
	FormatClaude          Format = "claude"
	FormatOpenAIChat      Format = "openai_chat"
	FormatOpenAIResponses Format = "openai_responses"
	FormatXAI             Format = "xai"
)
