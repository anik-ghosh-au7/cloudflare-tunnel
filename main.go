package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// TunnelCredentials holds the tunnel credentials.
type TunnelCredentials struct {
	AccountTag   string `json:"AccountTag"`
	TunnelSecret string `json:"TunnelSecret"`
	TunnelID     string `json:"TunnelID"`
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

// ensureDNSRecord ensures that the DNS record exists in Cloudflare for the tunnel.
func ensureDNSRecord(tunnelName, domain string) {
	log.Printf("Ensuring DNS record exists for %s...\n", domain)
	cmd := exec.Command("cloudflared", "tunnel", "route", "dns", tunnelName, domain)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("Failed to create or verify DNS record: %v", err)
	}
	log.Println("DNS record verified/created successfully.")
}

// writeConfigFile generates a Cloudflare Tunnel configuration file.
func writeConfigFile(tunnelName string, ports []int, domain, credentialsPath string) string {
	configPath := fmt.Sprintf("./%s-config.yml", tunnelName)
	file, err := os.Create(configPath)
	if err != nil {
		log.Fatalf("Failed to create config file: %v", err)
	}
	defer file.Close()

	fmt.Fprintf(file, "tunnel: %s\n", tunnelName)
	fmt.Fprintf(file, "credentials-file: %s\n", credentialsPath)
	fmt.Fprintln(file, "ingress:")

	// For each port, route under the same domain
	for _, port := range ports {
		fmt.Fprintf(file, "  - hostname: %s\n", domain)
		fmt.Fprintf(file, "    service: http://localhost:%d\n", port)
	}

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
	portsFlag := flag.String("ports", "", "Comma-separated list of ports to forward (e.g., 3000,5173)")
	tunnelName := flag.String("tunnel", "default-tunnel", "Cloudflare Tunnel name")
	domain := flag.String("domain", "anik.cc", "Root domain to route traffic (e.g., anik.cc)")
	credentialsPath := flag.String("credentials", "./credentials.json", "Path to the tunnel credentials file")
	flag.Parse()

	if *portsFlag == "" {
		log.Fatal("No ports specified. Use --ports to specify ports.")
	}

	ports := []int{}
	for _, portStr := range strings.Split(*portsFlag, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(portStr))
		if err != nil {
			log.Fatalf("Invalid port specified: %s", portStr)
		}
		ports = append(ports, port)
	}

	// Check for existing credentials
	creds, err := loadCredentials(*credentialsPath)
	if err != nil {
		log.Println("Credentials not found. Authenticating and creating a new tunnel...")
		authenticateCloudflare()
		creds = createTunnel(context.Background(), *tunnelName, *credentialsPath)
		if err := saveCredentials(*credentialsPath, creds); err != nil {
			log.Fatalf("Failed to save tunnel credentials: %v", err)
		}
	} else {
		log.Println("Reusing existing tunnel credentials.")
	}

	// Ensure the DNS record exists
	ensureDNSRecord(*tunnelName, *domain)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	configPath := writeConfigFile(creds.TunnelID, ports, *domain, *credentialsPath)

	tunnelCmd := startTunnel(ctx, configPath)

	// Handle graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	<-signalChan
	log.Println("Termination signal received...")

	cancel()
	tunnelCmd.Wait()
	log.Println("Cloudflare Tunnel stopped.")
}
