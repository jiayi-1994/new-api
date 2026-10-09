package service

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	// Bodies above this size are summarized instead of read into memory.
	taskRequestBodyReadLimit = 8 << 20
	// Stored bodies must fit MySQL TEXT (65535 bytes).
	taskRequestBodyStoreLimit = 60000
	// Longer string values are almost always inline media (base64); prompts fit.
	taskRequestBodyValueLimit = 8192
	taskRequestBodyValueKeep  = 256
)

// TaskRequestBodySnapshot captures the client's submit payload for the task
// details view. Inline media is shortened and multipart file parts are
// replaced by a name and size, so the stored copy stays small. It returns nil
// when the body cannot be read.
func TaskRequestBodySnapshot(c *gin.Context) *model.TaskRequestBody {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil
	}
	reader, err := storage.NewReader()
	if err != nil {
		return nil
	}
	defer reader.Close()

	contentType := c.GetHeader("Content-Type")
	record := &model.TaskRequestBody{
		ContentType: strings.ToValidUTF8(contentType[:min(len(contentType), 255)], ""), // varchar(255)
		Size:        storage.Size(),
	}
	mediaType, params, _ := mime.ParseMediaType(contentType)
	switch {
	case mediaType == "multipart/form-data":
		record.Body = summarizeMultipartBody(reader, params["boundary"])
	case storage.Size() > taskRequestBodyReadLimit:
		record.Body = fmt.Sprintf("[body not recorded: %d bytes]", storage.Size())
	default:
		raw, err := io.ReadAll(reader)
		if err != nil {
			return nil
		}
		record.Body = string(shortenJSONStrings(raw))
	}
	if len(record.Body) > taskRequestBodyStoreLimit {
		record.Body = record.Body[:taskRequestBodyStoreLimit] + "...[truncated]"
	}
	// PostgreSQL text rejects invalid UTF-8 and NUL bytes.
	record.Body = strings.ReplaceAll(strings.ToValidUTF8(record.Body, ""), "\x00", "")
	return record
}

// summarizeMultipartBody renders form fields as name=value lines. File parts
// are streamed to io.Discard and recorded only by file name and size.
func summarizeMultipartBody(r io.Reader, boundary string) string {
	if boundary == "" {
		return ""
	}
	reader := multipart.NewReader(r, boundary)
	var b strings.Builder
	for b.Len() <= taskRequestBodyStoreLimit {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FileName() != "" {
			size, _ := io.Copy(io.Discard, part)
			fmt.Fprintf(&b, "%s=[file %s, %d bytes]\n", part.FormName(), part.FileName(), size)
			continue
		}
		value, _ := io.ReadAll(io.LimitReader(part, taskRequestBodyValueLimit+1))
		if len(value) > taskRequestBodyValueLimit {
			value = append(value[:taskRequestBodyValueKeep], "...[truncated]"...)
		}
		fmt.Fprintf(&b, "%s=%s\n", part.FormName(), value)
	}
	return b.String()
}

// shortenJSONStrings cuts oversized JSON string literals in place, keeping
// key order and number text exactly as the client sent them. Malformed input
// is scanned safely and returned as is from the first unterminated string.
func shortenJSONStrings(raw []byte) []byte {
	var out bytes.Buffer
	last := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		end := i + 1
		for end < len(raw) && raw[end] != '"' {
			if raw[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(raw) {
			break
		}
		content := raw[i+1 : end]
		if len(content) > taskRequestBodyValueLimit {
			// Keep whole escapes and UTF-8 runes so the result stays valid JSON.
			keep := 0
			for keep < len(content) {
				step := 1
				if content[keep] == '\\' {
					step = 2
					if keep+1 < len(content) && content[keep+1] == 'u' {
						step = 6
					}
				} else if content[keep] >= utf8.RuneSelf {
					_, step = utf8.DecodeRune(content[keep:])
				}
				if keep+step > taskRequestBodyValueKeep {
					break
				}
				keep += step
			}
			out.Write(raw[last : i+1])
			out.Write(content[:keep])
			fmt.Fprintf(&out, "...[truncated %d bytes]\"", len(content))
			last = end + 1
		}
		i = end
	}
	if last == 0 {
		return raw
	}
	out.Write(raw[last:])
	return out.Bytes()
}
