// Package buderus enthält den REST-Client für die lokale API des Buderus-Gateways (z. B. MX400).
package buderus

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Pfade der Energiezähler für Heizung (central heating) und Warmwasser (domestic hot water).
const (
	PathHeatingConsumption = "/heatSources/emon/chConsumption"
	PathDHWConsumption     = "/heatSources/emon/dhwConsumption"
	PathCompressorPower    = "/heatSources/compressor/powerElecActual"
)

// maxBodySize begrenzt die gelesene Antwortgröße (1 MiB).
const maxBodySize = 1 << 20

// Client ruft Werte vom Gateway ab.
//
// Für Java-Entwickler: Ein Struct mit Methoden ist das Gegenstück zu einer Klasse.
// Kleingeschriebene Felder sind nur innerhalb des Packages sichtbar (≈ private).
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	logger     *slog.Logger
}

// NewClient erzeugt einen Client. Go hat keine Konstruktoren – New...-Funktionen sind die Konvention.
func NewClient(host string, port int, token string, logger *slog.Logger) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Das Gateway verwendet ein selbstsigniertes Zertifikat (entspricht curl --insecure).
	transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // selbstsigniertes Gateway-Zertifikat

	return &Client{
		baseURL: "https://" + net.JoinHostPort(host, strconv.Itoa(port)),
		token:   token,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
		logger: logger,
	}
}

// FetchEmon ruft einen Energiezähler-Endpunkt ab, z. B. PathHeatingConsumption.
//
// Go hat keine Exceptions: Fehler werden als zweiter Rückgabewert geliefert und
// müssen vom Aufrufer geprüft werden.
func (c *Client) FetchEmon(ctx context.Context, path string) (EmonResponse, error) {
	var result EmonResponse
	if err := c.getJSON(ctx, path, &result); err != nil {
		return EmonResponse{}, err
	}
	return result, nil
}

// FetchValue ruft einen Endpunkt ab, der einen einzelnen numerischen Wert liefert.
func (c *Client) FetchValue(ctx context.Context, path string) (ValueResponse, error) {
	var result ValueResponse
	if err := c.getJSON(ctx, path, &result); err != nil {
		return ValueResponse{}, err
	}
	return result, nil
}

func (c *Client) getJSON(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create request %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	// defer wird beim Verlassen der Funktion ausgeführt (≈ finally / try-with-resources).
	// Ein Fehler beim Schließen ist hier unkritisch und wird bewusst ignoriert.
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %s: %.512s", path, resp.Status, body)
	}

	// Rohantwort bei LOG_LEVEL=debug, hilfreich bei Gateways mit abweichenden Feldern.
	c.logger.Debug("gateway response", "path", path, "body", string(body))

	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
