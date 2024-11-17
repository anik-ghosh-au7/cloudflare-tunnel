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
	"syscall"
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
		return nil, err
	}
	var keys APIKeys
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, err
	}
	return &keys, nil
}

// loadCredentials reads and unmarshals the credentials file.
func loadCredentials(filePath string) (*TunnelCredentials, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var creds TunnelCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

// saveCredentials writes the credentials to a file.
func saveCredentials(filePath string, creds *TunnelCredentials) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
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

// createTunnel creates a new tunnel and saves the credentials.
func createTunnel(ctx context.Context, tunnelName, credentialsPath string) *TunnelCredentials {
	log.Printf("Creating tunnel: %s\n", tunnelName)
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--credentials-file", credentialsPath, "create", tunnelName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to create tunnel: %v", err)
	}
	log.Println("Tunnel created successfully.")
	creds, err := loadCredentials(credentialsPath)
	if err != nil {
		log.Fatalf("Failed to load tunnel credentials: %v", err)
	}
	return creds
}

// cloudflareAPIRequest makes a request to the Cloudflare API.
func cloudflareAPIRequest(method, url, apiToken string, payload []byte) ([]byte, error) {
	client := &http.Client{}
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Fatalf("Cloudflare API request failed: %s\n", body)
	}

	return io.ReadAll(resp.Body)
}

// dnsRecordExists checks if a DNS record for the domain already exists using Cloudflare API.
func dnsRecordExists(zoneID, domain, apiToken string) bool {
	log.Printf("Checking if DNS record exists for %s...\n", domain)
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s", zoneID, domain)
	resp, err := cloudflareAPIRequest("GET", url, apiToken, nil)
	if err != nil {
		log.Fatalf("Failed to query DNS record: %v", err)
	}

	var result struct {
		Success bool        `json:"success"`
		Result  []DNSRecord `json:"result"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		log.Fatalf("Failed to parse DNS record query response: %v", err)
	}

	return len(result.Result) > 0
}

// ensureDNSRecord ensures that the DNS record exists for the tunnel.
func ensureDNSRecord(zoneID, domain, tunnelID, apiToken string) {
	if dnsRecordExists(zoneID, domain, apiToken) {
		log.Println("DNS record already exists. Skipping creation.")
		return
	}

	log.Printf("Creating DNS record for %s...\n", domain)
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID)
	record := DNSRecord{
		Type:  "CNAME",
		Name:  domain,
		Value: tunnelID + ".cfargotunnel.com",
	}

	payload, _ := json.Marshal(record)
	_, err := cloudflareAPIRequest("POST", url, apiToken, payload)
	if err != nil {
		log.Fatalf("Failed to create DNS record: %v", err)
	}
	log.Println("DNS record created successfully.")
}

// writeConfigFile generates a Cloudflare Tunnel configuration file.
func writeConfigFile(tunnelName, tunnelID string, port int, domain, credentialsPath string) string {
	configPath := fmt.Sprintf("./%s-config.yml", tunnelID)
	file, err := os.Create(configPath)
	if err != nil {
		log.Fatalf("Failed to create config file: %v", err)
	}
	defer file.Close()

	fmt.Fprintf(file, "tunnel: %s\n", tunnelID)
	fmt.Fprintf(file, "credentials-file: %s\n", credentialsPath)
	fmt.Fprintln(file, "ingress:")

	fmt.Fprintf(file, "  - hostname: %s\n", domain)
	fmt.Fprintf(file, "    service: http://localhost:%d\n", port)
	fmt.Fprintln(file, "  - service: http_status:404")

	log.Printf("Config file created at: %s\n", configPath)
	return configPath
}

// startTunnel starts the Cloudflare Tunnel using the generated config.
func startTunnel(ctx context.Context, configPath string) *exec.Cmd {
	log.Println("Starting Cloudflare Tunnel...")
	cmd := exec.CommandContext(ctx, "cloudflared", "--config", configPath, "tunnel", "run")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatalf("Failed to start Cloudflare Tunnel: %v", err)
	}
	return cmd
}

func main() {
	portFlag := flag.Int("port", 0, "Port to forward (e.g., 5173)")
	tunnelName := flag.String("tunnel", "", "Cloudflare Tunnel name")
	domain := flag.String("domain", "", "Root domain to route traffic (e.g., anik.cc)")
	apiKeysPath := flag.String("apiKeys", "./api-keys.json", "Path to the API keys file")
	credentialsPath := flag.String("credentials", "./credentials.json", "Path to the tunnel credentials file")
	flag.Parse()

	if *portFlag == 0 || *tunnelName == "" || *domain == "" {
		log.Println("No arguments provided, attempting to load previous configuration.")
		creds, err := loadCredentials(*credentialsPath)
		if err != nil {
			log.Fatalf("Previous configuration not found and required arguments not passed: %v", err)
		}
		configPath := fmt.Sprintf("./%s-config.yml", creds.TunnelID)
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			log.Fatalf("Previous config file %s not found.", configPath)
		}
		log.Printf("Using previous configuration: %s", configPath)
		*portFlag = 0
		tunnelCmd := startTunnel(context.Background(), configPath)
		signalChan := make(chan os.Signal, 1)
		signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
		<-signalChan
		log.Println("Termination signal received...")
		tunnelCmd.Wait()
		return
	}

	apiKeys, err := loadAPIKeys(*apiKeysPath)
	if err != nil {
		log.Fatalf("Failed to load API keys: %v", err)
	}

	creds, err := loadCredentials(*credentialsPath)
	if err != nil {
		log.Println("Credentials not found. Authenticating and creating a new tunnel...")
		authenticateCloudflare()
		creds = createTunnel(context.Background(), *tunnelName, *credentialsPath)
		if err := saveCredentials(*credentialsPath, creds); err != nil {
			log.Fatalf("Failed to save tunnel credentials: %v", err)
		}
	}

	ensureDNSRecord(apiKeys.ZoneID, *domain, creds.TunnelID, apiKeys.ApiToken)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configPath := writeConfigFile(*tunnelName, creds.TunnelID, *portFlag, *domain, *credentialsPath)
	tunnelCmd := startTunnel(ctx, configPath)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	<-signalChan
	log.Println("Termination signal received...")
	cancel()
	tunnelCmd.Wait()
	log.Println("Cloudflare Tunnel stopped.")
}
