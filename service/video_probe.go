package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 输入参考视频时长探测：解析 ISO-BMFF（MP4/MOV）的 moov/mvhd 得到秒数。
// 探测结果直接成为计费乘数，因此所有读取都必须有界，所有秒数都必须过
// roundProbedVideoSeconds 的上界校验。

const (
	videoProbeHeadWindow  = 256 << 10 // faststart 的 moov 一般在最前面
	videoProbeMoovCap     = 4 << 20   // moov 本体读取上限
	videoProbeMaxHops     = 16        // top-level box 跳跃寻址步数上限
	videoProbeReqTimeout  = 15 * time.Second
	videoProbeTotalBudget = 45 * time.Second
)

var errMoovNotFound = errors.New("moov box not found")

// parseISOBMFFBoxHeader 返回 box 总长、类型与头部长度。size 为 -1 表示 box
// 延伸到文件末尾（size32==0）。
func parseISOBMFFBoxHeader(b []byte) (size int64, boxType string, headerLen int64, err error) {
	if len(b) < 8 {
		return 0, "", 0, errors.New("short box header")
	}
	size32 := binary.BigEndian.Uint32(b[0:4])
	boxType = string(b[4:8])
	headerLen = 8
	size = int64(size32)
	if size32 == 1 {
		if len(b) < 16 {
			return 0, "", 0, errors.New("short largesize box header")
		}
		size = int64(binary.BigEndian.Uint64(b[8:16]))
		headerLen = 16
	} else if size32 == 0 {
		size = -1
	}
	if size > 0 && size < headerLen {
		return 0, "", 0, fmt.Errorf("invalid box size %d for %q", size, boxType)
	}
	return size, boxType, headerLen, nil
}

// parseMvhdFromMoovPayload 在 moov 负载里找 mvhd 并换算秒数。
func parseMvhdFromMoovPayload(payload []byte) (float64, error) {
	off := int64(0)
	for off+8 <= int64(len(payload)) {
		size, typ, hdr, err := parseISOBMFFBoxHeader(payload[off:])
		if err != nil {
			return 0, err
		}
		if typ == "mvhd" {
			body := payload[off+hdr:]
			if len(body) < 4 {
				return 0, errors.New("short mvhd box")
			}
			switch version := body[0]; version {
			case 0:
				if len(body) < 20 {
					return 0, errors.New("short mvhd v0 box")
				}
				timescale := binary.BigEndian.Uint32(body[12:16])
				duration := binary.BigEndian.Uint32(body[16:20])
				if timescale == 0 {
					return 0, errors.New("mvhd timescale is zero")
				}
				return float64(duration) / float64(timescale), nil
			case 1:
				if len(body) < 32 {
					return 0, errors.New("short mvhd v1 box")
				}
				timescale := binary.BigEndian.Uint32(body[20:24])
				duration := binary.BigEndian.Uint64(body[24:32])
				if timescale == 0 {
					return 0, errors.New("mvhd timescale is zero")
				}
				return float64(duration) / float64(timescale), nil
			default:
				return 0, fmt.Errorf("unknown mvhd version %d", version)
			}
		}
		if size <= 0 {
			break
		}
		off += size
	}
	return 0, errMoovNotFound
}

// scanBufferForMoov 在一段从文件起始（或某个 box 边界）开始的连续字节里顺序
// 走 top-level box。返回 (seconds, found, err)。
func scanBufferForMoov(buf []byte) (float64, bool, error) {
	off := int64(0)
	for off+8 <= int64(len(buf)) {
		size, typ, hdr, err := parseISOBMFFBoxHeader(buf[off:])
		if err != nil {
			return 0, false, err
		}
		if typ == "moov" {
			end := int64(len(buf))
			if size > 0 && off+size < end {
				end = off + size
			}
			seconds, err := parseMvhdFromMoovPayload(buf[off+hdr : end])
			if err != nil {
				return 0, false, err
			}
			return seconds, true, nil
		}
		if size <= 0 {
			break
		}
		off += size
	}
	return 0, false, nil
}

// roundProbedVideoSeconds 把探测出的时长换成计费秒数：四舍五入、最少 1 秒、
// 超过单视频上限直接报错（计费乘数不做静默 clamp）。
func roundProbedVideoSeconds(seconds float64) (int, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, fmt.Errorf("invalid probed video duration %v", seconds)
	}
	// 上限必须在 float→int 之前检查：超大 mvhd duration 转 int 会溢出为
	// 负数，随后被"最少 1 秒"兜底洗白，绕过上限。
	if seconds > float64(relaycommon.MaxInputReferenceVideoSeconds)+0.5 {
		return 0, fmt.Errorf("input reference video duration %.0f exceeds the %d second limit", seconds, relaycommon.MaxInputReferenceVideoSeconds)
	}
	rounded := int(math.Round(seconds))
	if rounded < 1 {
		rounded = 1
	}
	if rounded > relaycommon.MaxInputReferenceVideoSeconds {
		return 0, fmt.Errorf("input reference video duration %d exceeds the %d second limit", rounded, relaycommon.MaxInputReferenceVideoSeconds)
	}
	return rounded, nil
}

// VideoDurationSecondsFromReader 流式解析一个本地已有的视频（multipart 上传
// 文件），非 moov 的 top-level box 直接丢弃跳过。
func VideoDurationSecondsFromReader(r io.Reader) (int, error) {
	header := make([]byte, 16)
	for hops := 0; hops < videoProbeMaxHops; hops++ {
		if _, err := io.ReadFull(r, header[:8]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return 0, errMoovNotFound
			}
			return 0, err
		}
		consumed := int64(8)
		if binary.BigEndian.Uint32(header[0:4]) == 1 {
			if _, err := io.ReadFull(r, header[8:16]); err != nil {
				return 0, errors.New("short largesize box header")
			}
			consumed = 16
		}
		size, typ, hdr, err := parseISOBMFFBoxHeader(header[:consumed])
		if err != nil {
			return 0, err
		}
		if typ == "moov" {
			payloadLen := int64(videoProbeMoovCap)
			if size > 0 {
				payloadLen = size - hdr
			}
			if payloadLen > videoProbeMoovCap {
				payloadLen = videoProbeMoovCap
			}
			payload, err := io.ReadAll(io.LimitReader(r, payloadLen))
			if err != nil {
				return 0, err
			}
			seconds, err := parseMvhdFromMoovPayload(payload)
			if err != nil {
				return 0, err
			}
			return roundProbedVideoSeconds(seconds)
		}
		if size <= 0 {
			return 0, errMoovNotFound
		}
		if _, err := io.CopyN(io.Discard, r, size-consumed); err != nil {
			return 0, errMoovNotFound
		}
	}
	return 0, errMoovNotFound
}

// videoProbeRangeGet 拉取 [start, end]，返回数据与（可得时的）文件总长。
// 服务器忽略 Range 返回 200 时也只读窗口大小的字节。
func videoProbeRangeGet(ctx context.Context, client *http.Client, url string, start, end int64) ([]byte, int64, error) {
	reqCtx, cancel := context.WithTimeout(ctx, videoProbeReqTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("unexpected status %s fetching video header", resp.Status)
	}
	totalSize := int64(-1)
	if resp.StatusCode == http.StatusPartialContent {
		var s, e int64
		if _, scanErr := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &s, &e, &totalSize); scanErr != nil {
			totalSize = -1
		}
	} else if resp.ContentLength > 0 {
		totalSize = resp.ContentLength
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, end-start+1))
	if err != nil {
		return nil, 0, err
	}
	return data, totalSize, nil
}

// ProbeVideoDurationSeconds 探测远端视频 URL 的时长（计费秒数）。
// 策略：头部窗口找 faststart 的 moov；没有则依据总长从头部窗口内已知的
// box 边界开始按 box 头跳跃寻址（每跳 16 字节），命中 moov 后只拉其本体。
// URL 是用户可控输入，必须走 SSRF 防护校验与客户端。
func ProbeVideoDurationSeconds(ctx context.Context, rawURL string) (int, error) {
	trimmed := strings.TrimSpace(rawURL)
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return 0, fmt.Errorf("input reference video must be an http(s) URL")
	}
	if err := ValidateSSRFProtectedFetchURL(trimmed); err != nil {
		return 0, err
	}
	client := GetSSRFProtectedHTTPClient()
	if client == nil {
		client = http.DefaultClient
	}

	ctx, cancel := context.WithTimeout(ctx, videoProbeTotalBudget)
	defer cancel()

	head, totalSize, err := videoProbeRangeGet(ctx, client, trimmed, 0, videoProbeHeadWindow-1)
	if err != nil {
		return 0, err
	}
	if seconds, found, scanErr := scanBufferForMoov(head); scanErr == nil && found {
		return roundProbedVideoSeconds(seconds)
	}

	if totalSize <= 0 {
		return 0, fmt.Errorf("moov box is not in the head window and the server did not report a total size")
	}
	// 推进到头部窗口能定位到的最后一个 box 边界；退出时 off 一定是合法
	// 的 top-level box 起点（可能已越过窗口，如被跳过的 mdat 的末尾）。
	off := int64(0)
	for off+8 <= int64(len(head)) {
		size, _, _, hdrErr := parseISOBMFFBoxHeader(head[off:])
		if hdrErr != nil || size <= 0 {
			break
		}
		off += size
	}
	for hops := 0; hops < videoProbeMaxHops && off+8 <= totalSize; hops++ {
		headerBytes, _, hdrErr := videoProbeRangeGet(ctx, client, trimmed, off, off+15)
		if hdrErr != nil {
			return 0, hdrErr
		}
		size, typ, hdr, parseErr := parseISOBMFFBoxHeader(headerBytes)
		if parseErr != nil {
			return 0, parseErr
		}
		if typ == "moov" {
			end := off + size - 1
			if size <= 0 || size-hdr > videoProbeMoovCap {
				end = off + hdr + videoProbeMoovCap - 1
			}
			if end >= totalSize {
				end = totalSize - 1
			}
			moovPayload, _, moovErr := videoProbeRangeGet(ctx, client, trimmed, off+hdr, end)
			if moovErr != nil {
				return 0, moovErr
			}
			seconds, mvhdErr := parseMvhdFromMoovPayload(moovPayload)
			if mvhdErr != nil {
				return 0, mvhdErr
			}
			return roundProbedVideoSeconds(seconds)
		}
		if size <= 0 {
			break
		}
		off += size
	}
	return 0, errMoovNotFound
}
