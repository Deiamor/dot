package asset

type AssetId = string

type Asset struct {
	AssetId     string
	Symbol      string
	Decimals    int
	Description string
}

func NewAsset(id, symbol string, decimals int) Asset {
	return Asset{AssetId: id, Symbol: symbol, Decimals: decimals}
}

var (
	BTC  = NewAsset("BTC", "BTC", 8)
	USDC = NewAsset("USDC", "USDC", 6)
)
