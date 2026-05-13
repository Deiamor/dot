package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	binanceFapiBase    = "https://fapi.binance.com"
	klineEndpoint      = "/fapi/v1/klines"
	klineChunkSize     = 1000 // Binance max per request
)

// DownloadProgress is emitted during a download to report progress.
type DownloadProgress struct {
	Symbol    string
	Interval  string
	Done      int
	Total     int // 0 = unknown
	FilePath  string
	Error     error
	Completed bool
}

// DownloadRequest describes a kline download job submitted via the dashboard UI.
type DownloadRequest struct {
	Symbol    string    `json:"symbol"`
	Interval  string    `json:"interval"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
}

// DatasetInfo describes a kline file available for backtesting.
type DatasetInfo struct {
	Filename  string    `json:"filename"`
	Symbol    string    `json:"symbol"`
	Interval  string    `json:"interval"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Candles   int       `json:"candles"`
	Path      string    `json:"path"`
}

// DownloadKlines fetches klines from Binance in 1000-candle chunks and writes
// a single JSON file to dataDir. Progress is sent on the returned channel,
// which is closed when the download completes or fails.
func DownloadKlines(ctx context.Context, req DownloadRequest, dataDir string) <-chan DownloadProgress {
	ch := make(chan DownloadProgress, 32)
	go func() {
		defer close(ch)
		path, err := downloadKlines(ctx, req, dataDir, func(done, total int) {
			ch <- DownloadProgress{
				Symbol:   req.Symbol,
				Interval: req.Interval,
				Done:     done,
				Total:    total,
			}
		})
		if err != nil {
			ch <- DownloadProgress{Symbol: req.Symbol, Interval: req.Interval, Error: err}
			return
		}
		ch <- DownloadProgress{
			Symbol:    req.Symbol,
			Interval:  req.Interval,
			FilePath:  path,
			Completed: true,
		}
	}()
	return ch
}

func downloadKlines(ctx context.Context, req DownloadRequest, dataDir string, progress func(done, total int)) (string, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dataDir, err)
	}

	startMs := req.StartTime.UnixMilli()
	endMs := req.EndTime.UnixMilli()
	if endMs <= startMs {
		return "", fmt.Errorf("EndTime must be after StartTime")
	}

	// Estimate total chunks.
	intervalMs := intervalDuration(req.Interval).Milliseconds()
	if intervalMs <= 0 {
		return "", fmt.Errorf("unknown interval %q", req.Interval)
	}
	totalCandles := int((endMs-startMs)/intervalMs) + 1
	total := (totalCandles + klineChunkSize - 1) / klineChunkSize

	var all []json.RawMessage
	cursor := startMs
	chunk := 0

	hc := &http.Client{Timeout: 30 * time.Second}
	for cursor < endMs {
		chunkEnd := cursor + int64(klineChunkSize)*intervalMs
		if chunkEnd > endMs {
			chunkEnd = endMs
		}

		params := url.Values{
			"symbol":    {req.Symbol},
			"interval":  {req.Interval},
			"startTime": {strconv.FormatInt(cursor, 10)},
			"endTime":   {strconv.FormatInt(chunkEnd, 10)},
			"limit":     {strconv.Itoa(klineChunkSize)},
		}
		u := binanceFapiBase + klineEndpoint + "?" + params.Encode()
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}

		resp, err := hc.Do(httpReq)
		if err != nil {
			return "", fmt.Errorf("chunk %d: %w", chunk, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", fmt.Errorf("chunk %d read: %w", chunk, err)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("chunk %d HTTP %d: %s", chunk, resp.StatusCode, body)
		}

		var rows []json.RawMessage
		if err := json.Unmarshal(body, &rows); err != nil {
			return "", fmt.Errorf("chunk %d decode: %w", chunk, err)
		}
		all = append(all, rows...)

		if len(rows) == 0 {
			break
		}

		// Advance cursor past the last returned candle.
		var lastRow []json.RawMessage
		if err := json.Unmarshal(rows[len(rows)-1], &lastRow); err == nil && len(lastRow) >= 7 {
			var closeTime int64
			if err := json.Unmarshal(lastRow[6], &closeTime); err == nil {
				cursor = closeTime + 1
			} else {
				cursor = chunkEnd + 1
			}
		} else {
			cursor = chunkEnd + 1
		}

		chunk++
		progress(chunk, total)
	}

	if len(all) == 0 {
		return "", fmt.Errorf("no klines returned for %s %s", req.Symbol, req.Interval)
	}

	filename := fmt.Sprintf("%s_%s_%s_%s.json",
		strings.ToUpper(req.Symbol),
		req.Interval,
		req.StartTime.Format("20060102"),
		req.EndTime.Format("20060102"),
	)
	path := filepath.Join(dataDir, filename)
	out, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetIndent("", "")
	if err := enc.Encode(all); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	return path, nil
}

// ListDatasets scans dataDir for downloaded kline files and returns metadata for each.
func ListDatasets(dataDir string) ([]DatasetInfo, error) {
	entries, err := os.ReadDir(dataDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []DatasetInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := parseDatasetFilename(entry.Name(), filepath.Join(dataDir, entry.Name()))
		if err != nil {
			continue // skip files that don't match naming convention
		}
		out = append(out, info)
	}
	return out, nil
}

// parseDatasetFilename decodes "{SYMBOL}_{interval}_{YYYYMMDD}_{YYYYMMDD}.json".
func parseDatasetFilename(name, path string) (DatasetInfo, error) {
	base := strings.TrimSuffix(name, ".json")
	parts := strings.Split(base, "_")
	if len(parts) < 4 {
		return DatasetInfo{}, fmt.Errorf("unexpected filename format")
	}
	start, err := time.Parse("20060102", parts[len(parts)-2])
	if err != nil {
		return DatasetInfo{}, err
	}
	end, err := time.Parse("20060102", parts[len(parts)-1])
	if err != nil {
		return DatasetInfo{}, err
	}
	interval := parts[len(parts)-3]
	symbol := strings.Join(parts[:len(parts)-3], "_")

	// Count candles.
	f, err := os.Open(path)
	candles := 0
	if err == nil {
		var rows []json.RawMessage
		if json.NewDecoder(f).Decode(&rows) == nil {
			candles = len(rows)
		}
		f.Close()
	}

	return DatasetInfo{
		Filename:  name,
		Symbol:    symbol,
		Interval:  interval,
		StartTime: start,
		EndTime:   end,
		Candles:   candles,
		Path:      path,
	}, nil
}

// intervalDuration returns the wall-clock duration for a Binance interval string.
func intervalDuration(interval string) time.Duration {
	switch interval {
	case "1m":
		return time.Minute
	case "3m":
		return 3 * time.Minute
	case "5m":
		return 5 * time.Minute
	case "15m":
		return 15 * time.Minute
	case "30m":
		return 30 * time.Minute
	case "1h":
		return time.Hour
	case "2h":
		return 2 * time.Hour
	case "4h":
		return 4 * time.Hour
	case "6h":
		return 6 * time.Hour
	case "8h":
		return 8 * time.Hour
	case "12h":
		return 12 * time.Hour
	case "1d":
		return 24 * time.Hour
	case "3d":
		return 72 * time.Hour
	case "1w":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}
