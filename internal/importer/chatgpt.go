package importer

// ParseChatGPTHTML extracts the selected visible conversation path from JSON
// embedded in a ChatGPT public share page. It never executes page JavaScript.
func ParseChatGPTHTML(data []byte) (Conversation, error) {
	return parseShareHTML(data, SourceChatGPTShare)
}
