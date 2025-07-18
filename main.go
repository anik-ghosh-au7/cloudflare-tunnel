package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/armon/go-socks5"
)

// DNSRecord holds the information for Cloudflare DNS records.
type DNSRecord struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"content"`
}

// TunnelCredentials holds the tunnel credentials.
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelSecret string `json:"TunnelSecret"`
	TunnelID     string `json:"TunnelID"`
}

// APIKeys holds the API Token and Zone ID from the JSON file.
type APIKeys struct {
	ApiToken string `json:"ApiToken"`
	ZoneID   string `json:"ZoneId"`
}

// loadAPIKeys loads the API keys from the specified file.
func loadAPIKeys(filePath string) (*APIKeys, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read API keys file: %w", err)
	}
	var keys APIKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("failed to unmarshal API keys: %w", err)
	}
	if keys.ApiToken == "" || keys.ZoneID == "" {
		return nil, fmt.Errorf("ApiToken or ZoneId missing in API keys file")
	}
	return &keys, nil
}

// loadCredentials reads and unmarshals the credentials file.
func loadCredentials(filePath string) (*TunnelCredentials, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}
	var creds TunnelCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials: %w", err)
	}
	return &creds, nil
}

// saveCredentials writes the credentials to a file.
func saveCredentials(filePath string, creds *TunnelCredentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	return os.WriteFile(filePath, data, 0600)
}

// authenticateCloudflare performs authentication with Cloudflare.
func authenticateCloudflare() {
	log.Println("Authenticating with Cloudflare...")
	cmd := exec.Command("cloudflared", "tunnel", "login")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Cloudflare authentication failed: %v", err)
	}
	log.Println("Cloudflare authentication successful.")
}

// createTunnel creates a new tunnel and returns its credentials.
func createTunnel(ctx context.Context, tunnelName, credentialsPath string) *TunnelCredentials {
	log.Printf("Creating tunnel %s...\n", tunnelName)
	cmd := exec.CommandContext(ctx,
		"cloudflared", "tunnel",
		"--credentials-file", credentialsPath,
		"create", tunnelName,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to create tunnel: %v", err)
	}
	creds, err := loadCredentials(credentialsPath)
	if err != nil {
		log.Fatalf("Failed to load tunnel credentials: %v", err)
	}
	log.Println("Tunnel created successfully.")
	return creds
}

// cloudflareAPIRequest makes a request to the Cloudflare API.
func cloudflareAPIRequest(method, url, apiToken string, payload []byte) ([]byte, error) {
	client := &http.Client{}
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("failed to create API request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error: %s", body)
	}
	return body, nil
}

// dnsRecordExists checks if a DNS record for the domain already exists.
func dnsRecordExists(zoneID, domain, apiToken string) bool {
	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s",
		zoneID, domain,
	)
	resp, err := cloudflareAPIRequest("GET", url, apiToken, nil)
	if err != nil {
		log.Fatalf("Failed to query DNS record: %v", err)
	}
	var result struct {
		Success bool        `json:"success"`
		Result  []DNSRecord `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		log.Fatalf("Failed to parse DNS record response: %v", err)
	}
	return len(result.Result) > 0
}

// ensureDNSRecord ensures that the DNS record exists for the tunnel.
func ensureDNSRecord(zoneID, domain, tunnelID, apiToken string) {
	if dnsRecordExists(zoneID, domain, apiToken) {
		log.Printf("DNS record for %s already exists, skipping.\n", domain)
		return
	}
	record := DNSRecord{
		Type:  "CNAME",
		Name:  domain,
		Value: tunnelID + ".cfargotunnel.com",
	}
	payload, err := json.Marshal(record)
	if err != nil {
		log.Fatalf("Failed to marshal DNS record: %v", err)
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
	if _, err := cloudflareAPIRequest("POST", url, apiToken, payload); err != nil {
		log.Fatalf("Failed to create DNS record: %v", err)
	}
	log.Printf("Created DNS record for %s.\n", domain)
}

// startTunnel starts the Cloudflare Tunnel using the given config file.
func startTunnel(ctx context.Context, configPath string) *exec.Cmd {
	cmd := exec.CommandContext(ctx,
		"cloudflared", "--config", configPath, "tunnel", "run",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start Cloudflare Tunnel: %v", err)
	}
	log.Println("Cloudflared tunnel started.")
	return cmd
}

// logoutCloudflare logs out of Cloudflare.
func logoutCloudflare() {
	log.Println("Logging out of Cloudflare...")
	cmd := exec.Command("cloudflared", "tunnel", "logout")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("Logout failed: %v", err)
	} else {
		log.Println("Successfully logged out.")
	}
}

// Route ties a hostname to a service URI.
type Route struct {
	Hostname string
	Service  string
}

// writeConfigFile writes a multi‑rule ingress YAML for the tunnel.
func writeConfigFile(
	tunnelID, credentialsPath string,
	routes []Route,
) string {
	configPath := fmt.Sprintf("./%s-config.yml", tunnelID)
	f, err := os.Create(configPath)
	if err != nil {
		log.Fatalf("Failed to create config file: %v", err)
	}
	defer f.Close()

	fmt.Fprintf(f, "tunnel: %s\n", tunnelID)
	fmt.Fprintf(f, "credentials-file: %s\n", credentialsPath)
	fmt.Fprintln(f, "ingress:")
	for _, r := range routes {
		fmt.Fprintf(f, "  - hostname: %s\n", r.Hostname)
		fmt.Fprintf(f, "    service: %s\n", r.Service)
	}

	// fallback: HTTP 404 for HTTP ingress, else drop TCP
	fallback := "tcp://localhost:0"
	if strings.HasPrefix(routes[0].Service, "http://") ||
		strings.HasPrefix(routes[0].Service, "https://") {
		fallback = "http_status:404"
	}
	fmt.Fprintf(f, "  - service: %s\n", fallback)

	log.Printf("Config file written to %s\n", configPath)
	return configPath
}

// startSocks5 spins up a SOCKS5 proxy handling both TCP & UDP.
func startSocks5(port int) {
	conf := &socks5.Config{}
	server, err := socks5.New(conf)
	if err != nil {
		log.Fatalf("Failed to create SOCKS5 server: %v", err)
	}
	go func() {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		log.Printf("SOCKS5 proxy listening on %s\n", addr)
		if err := server.ListenAndServe("tcp", addr); err != nil {
			log.Fatalf("SOCKS5 server error: %v", err)
		}
	}()
}

func main() {
	portFlag := flag.Int("port", 0, "Port to forward (e.g., 22, 5173)")
	tunnelName := flag.String("tunnel", "", "Cloudflare Tunnel name")
	domain := flag.String("domain", "", "Root domain (e.g., anik.cc)")
	apiKeysPath := flag.String("apiKeys", "./api-keys.json", "Path to API keys file")
	credentialsPath := flag.String("credentials", "./credentials.json", "Path to tunnel credentials file")
	protocol := flag.String("protocol", "http", "Protocol to forward: http, tcp, or all")
	socks5Port := flag.Int("socks5-port", 1080, "Local SOCKS5 port (only for protocol=all)")
	proxyDomain := flag.String("proxy-domain", "", "Hostname for SOCKS5 proxy (protocol=all)")

	flag.Parse()

	// reuse previous config if flags missing
	if *portFlag == 0 || *tunnelName == "" || *domain == "" {
		creds, err := loadCredentials(*credentialsPath)
		if err != nil {
			log.Fatalf("Previous configuration not found: %v", err)
		}
		cfg := fmt.Sprintf("./%s-config.yml", creds.TunnelID)
		if _, err := os.Stat(cfg); os.IsNotExist(err) {
			log.Fatalf("Config %s not found", cfg)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		defer logoutCloudflare()
		cmd := startTunnel(ctx, cfg)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		cancel()
		cmd.Wait()
		return
	}

	// load or create tunnel + DNS
	apiKeys, err := loadAPIKeys(*apiKeysPath)
	if err != nil {
		log.Fatalf("Failed to load API keys: %v", err)
	}
	creds, err := loadCredentials(*credentialsPath)
	if err != nil {
		authenticateCloudflare()
		creds = createTunnel(context.Background(), *tunnelName, *credentialsPath)
		if err := saveCredentials(*credentialsPath, creds); err != nil {
			log.Fatalf("Failed to save credentials: %v", err)
		}
	}
	ensureDNSRecord(apiKeys.ZoneID, *domain, creds.TunnelID, apiKeys.ApiToken)

	// build ingress routes
	var routes []Route
	switch *protocol {
	case "http":
		routes = []Route{{Hostname: *domain, Service: fmt.Sprintf("http://localhost:%d", *portFlag)}}
	case "tcp":
		routes = []Route{{Hostname: *domain, Service: fmt.Sprintf("tcp://localhost:%d", *portFlag)}}
	case "all":
		if *proxyDomain == "" {
			log.Fatal("proxy-domain is required when protocol=all")
		}
		// ensure DNS for proxy
		ensureDNSRecord(apiKeys.ZoneID, *proxyDomain, creds.TunnelID, apiKeys.ApiToken)
		routes = append(routes, Route{*domain, fmt.Sprintf("tcp://localhost:%d", *portFlag)})
		routes = append(routes, Route{*proxyDomain, fmt.Sprintf("tcp://localhost:%d", *socks5Port)})
	default:
		log.Fatalf("Unsupported protocol: %s", *protocol)
	}

	// write config & start services
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer logoutCloudflare()

	configPath := writeConfigFile(creds.TunnelID, *credentialsPath, routes)
	if *protocol == "all" {
		startSocks5(*socks5Port)
	}
	cmd := startTunnel(ctx, configPath)

	// wait for interrupt
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	cancel()
	cmd.Wait()
	log.Println("Cloudflare Tunnel stopped.")
}
