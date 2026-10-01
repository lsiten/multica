package communications

import (
	"bytes"
	"github.com/emersion/go-message/mail"
	"io"
	"strings"
)

func readableMailBody(raw []byte) string {
	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		return "[Mail body could not be decoded]"
	}
	defer reader.Close()
	for n := 0; n < 50; n++ {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		header, ok := part.Header.(*mail.InlineHeader)
		if !ok {
			continue
		}
		contentType, _, err := header.ContentType()
		if err != nil || contentType != "text/plain" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part.Body, 64<<10))
		if err != nil {
			return "[Mail body could not be decoded]"
		}
		return strings.ToValidUTF8(string(data), "�")
	}
	return "[No plain-text body in this bounded message excerpt]"
}
