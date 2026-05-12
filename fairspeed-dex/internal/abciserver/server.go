package abciserver

import (
	"fmt"

	cmtserver "github.com/cometbft/cometbft/abci/server"
	cmtlog "github.com/cometbft/cometbft/libs/log"
	cmtservice "github.com/cometbft/cometbft/libs/service"

	localabci "github.com/byunghee1994/fairspeed-dex/internal/abci"
)

// Transport selects the ABCI server protocol.
type Transport string

const (
	TransportSocket Transport = "socket" // Unix socket / TCP (default, simpler)
	TransportGRPC   Transport = "grpc"   // gRPC (required for CometBFT v1+)
)

// StartServer creates a CometBFT ABCI server and starts it.
// addr is the listen address, e.g. "tcp://0.0.0.0:26658" or "unix:///tmp/app.sock".
// Returns the running service; caller is responsible for calling Stop().
func StartServer(app *localabci.DEXApplication, addr string, transport Transport) (cmtservice.Service, error) {
	adapter := NewCometBFTAdapter(app)
	srv, err := cmtserver.NewServer(addr, string(transport), adapter)
	if err != nil {
		return nil, fmt.Errorf("create ABCI server: %w", err)
	}
	srv.SetLogger(cmtlog.NewNopLogger())
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("start ABCI server: %w", err)
	}
	return srv, nil
}
