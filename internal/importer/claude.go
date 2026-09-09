package importer

// ParseClaudeHTML extracts explicit human/assistant messages from JSON embedded
// in a Claude public share page. It never executes page JavaScript.
func ParseClaudeHTML(data []byte) (Conversation, error) {
	return parseShareHTML(data, SourceClaudeShare)
}
