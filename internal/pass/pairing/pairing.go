package pairing

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
	"github.com/twofas/2fas-server/internal/common/logging"
)

type Pairing struct {
	store store
}

type store interface {
	AddExtension(ctx context.Context, extensionID string)
	ExtensionExists(ctx context.Context, extensionID string) bool
	GetPairingInfo(ctx context.Context, extensionID string) (PairingInfo, error)
	SetPairingInfo(ctx context.Context, extensionID string, pi PairingInfo) error
}

func NewPairingApp() *Pairing {
	return &Pairing{
		store: NewMemoryStore(),
	}
}

type ConfigureBrowserExtensionRequest struct {
	ExtensionID string `json:"extension_id"`
}

type ConfigureBrowserExtensionResponse struct {
	BrowserExtensionPairingToken string `json:"browser_extension_pairing_token"`
	ConnectionToken              string `json:"connection_token"`
}

func (p *Pairing) ConfigureBrowserExtension(ctx context.Context, req ConfigureBrowserExtensionRequest) (ConfigureBrowserExtensionResponse, error) {
	p.store.AddExtension(ctx, req.ExtensionID)
	// TODO: generate connection token and pairing token.
	connectionToken := req.ExtensionID
	pairingToken := req.ExtensionID

	return ConfigureBrowserExtensionResponse{
		ConnectionToken:              connectionToken,
		BrowserExtensionPairingToken: pairingToken,
	}, nil
}

type ExtensionWaitForConnectionInput struct {
	ResponseWriter http.ResponseWriter
	HttpReq        *http.Request
}

type WaitForConnectionResponse struct {
	BrowserExtensionProxyToken string `json:"browser_extension_proxy_token"`
	Status                     string `json:"status"`
	DeviceID                   string `json:"device_id"`
}

func (p *Pairing) ServePairingWS(w http.ResponseWriter, r *http.Request, extID string) {
	log := logging.WithField("extension_id", extID)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade on ServePairingWS: %v", err)
		return
	}
	defer conn.Close()

	log.Info("Starting pairing WS")

	if deviceID, pairingDone := p.isExtensionPaired(r.Context(), extID, log); pairingDone {
		if err := p.sendTokenAndCloseConn(extID, deviceID, conn); err != nil {
			log.Errorf("Failed to send token: %v", err)
		}
		return
	}

	const (
		maxWaitTime              = 3 * time.Minute
		checkIfConnectedInterval = time.Second
	)
	maxWaitC := time.After(maxWaitTime)
	// TODO: consider returning event from store on change.
	connectedCheckTicker := time.NewTicker(checkIfConnectedInterval)
	defer connectedCheckTicker.Stop()
	for {
		select {
		case <-maxWaitC:
			log.Info("Closing paring ws after timeout")
			return
		case <-connectedCheckTicker.C:
			if deviceID, pairingDone := p.isExtensionPaired(r.Context(), extID, log); pairingDone {
				if err := p.sendTokenAndCloseConn(extID, deviceID, conn); err != nil {
					log.Errorf("Failed to send token: %v", err)
					return
				}
				log.WithField("device_id", deviceID).Infof("Paring ws finished")
				return
			}
		}
	}
}

func (p *Pairing) isExtensionPaired(ctx context.Context, extID string, log *logrus.Entry) (string, bool) {
	pairingInfo, err := p.store.GetPairingInfo(ctx, extID)
	if err != nil {
		log.Warn("Failed to get pairing info")
		return "", false
	}
	return pairingInfo.Device.DeviceID, pairingInfo.IsPaired()
}

func (p *Pairing) sendTokenAndCloseConn(extID, deviceID string, conn *websocket.Conn) error {
	// generate token here
	if err := conn.WriteJSON(WaitForConnectionResponse{
		// TODO: replace with real token.
		BrowserExtensionProxyToken: extID,
		Status:                     "ok",
		DeviceID:                   deviceID,
	}); err != nil {
		return fmt.Errorf("failed to write to extension: %v", err)
	}
	return conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
}

// GetPairingInfo returns paired device and information if pairing was done.
func (p *Pairing) GetPairingInfo(ctx context.Context, extensionID string) (PairingInfo, error) {
	return p.store.GetPairingInfo(ctx, extensionID)
}

type ConfirmPairingRequest struct {
	FCMToken string `json:"fcm_token"`
	DeviceID string `json:"device_id"`
}

func (p *Pairing) ConfirmPairing(ctx context.Context, req ConfirmPairingRequest, extensionID string) error {
	return p.store.SetPairingInfo(ctx, extensionID, PairingInfo{
		Device: MobileDevice{
			DeviceID: req.DeviceID,
			FCMToken: req.FCMToken,
		},
		PairedAt: time.Now().UTC(),
	})
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4 * 1024,
	WriteBufferSize: 4 * 1024,
	CheckOrigin: func(r *http.Request) bool {
		allowedOrigin := os.Getenv("WEBSOCKET_ALLOWED_ORIGIN")

		if allowedOrigin != "" {
			return r.Header.Get("Origin") == allowedOrigin
		}

		return true
	},
}