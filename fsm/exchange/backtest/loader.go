package backtest

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/deiamor/perp-strategy-engine/fsm/exchange"
)

// KlineRecord represents one candle as returned by the Binance
// GET /fapi/v1/klines endpoint.
//
// JSON layout: [openTime, open, high, low, close, volume, closeTime,
//
//	quoteAssetVolume, numTrades, takerBuyBaseVol, takerBuyQuoteVol, ignore]
type KlineRecord [12]json.RawMessage

// LoadSnapshots reads a JSON file written by the downloader and converts
// each kline into a MarketSnapshot.
//
// Mid   = (high + low) / 2
// Bid   = mid × (1 − 0.5 bps)  — synthetic half-spread
// Ask   = mid × (1 + 0.5 bps)
// FundingRate is 0 unless the file contains a parallel funding array (future).
func LoadSnapshots(path, symbol string) ([]exchange.MarketSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var raw []KlineRecord
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	snaps := make([]exchange.MarketSnapshot, 0, len(raw))
	for i, k := range raw {
		s, err := klineToSnapshot(k, symbol)
		if err != nil {
			return nil, fmt.Errorf("kline[%d]: %w", i, err)
		}
		snaps = append(snaps, s)
	}
	return snaps, nil
}

func klineToSnapshot(k KlineRecord, symbol string) (exchange.MarketSnapshot, error) {
	openTimeMs, err := parseInt64(k[0])
	if err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("openTime: %w", err)
	}
	high, err := parseFloatJSON(k[2])
	if err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("high: %w", err)
	}
	low, err := parseFloatJSON(k[3])
	if err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("low: %w", err)
	}
	close_, err := parseFloatJSON(k[4])
	if err != nil {
		return exchange.MarketSnapshot{}, fmt.Errorf("close: %w", err)
	}

	mid := (high + low) / 2
	halfSpread := mid * 0.00005 // 0.5 bps synthetic spread
	return exchange.MarketSnapshot{
		Symbol:    symbol,
		Mid:       mid,
		Bid:       mid - halfSpread,
		Ask:       mid + halfSpread,
		MarkPrice: close_,
		Timestamp: time.UnixMilli(openTimeMs),
	}, nil
}

func parseInt64(raw json.RawMessage) (int64, error) {
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	// Binance sometimes sends numbers as strings.
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseFloatJSON(raw json.RawMessage) (float64, error) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}
	return strconv.ParseFloat(s, 64)
}
