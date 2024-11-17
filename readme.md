# `cloudflare-tunnel`

The `cloudflare-tunnel` simplifies creating and managing Cloudflare Tunnels to route traffic from your local machine to a custom domain.

---

## Features

- Automatically authenticate and create Cloudflare Tunnels.
- Route traffic to a specific local port through a custom domain.
- Manage DNS records using Cloudflare API.
- Ensure secure and seamless public access to your local services.

---

## Prerequisites

1. **Cloudflare Account**:
   - [Sign up](https://www.cloudflare.com/) if you don't have one.
2. **Cloudflare API Token**:

   - Create an API token with the following permissions:
     - **Zone:DNS:Edit**
     - **Zone:DNS:Read**

3. **Cloudflare Tunnel Installed**:
   - Install `cloudflared`:
     ```bash
     brew install cloudflare/cloudflare/cloudflared
     ```

---

## Setup Instructions

### 1. Clone the Repository

```bash
git clone <repo-url>
cd cloudflare-tunnel
```

### 2. Create Necessary Files

Copy the sample configuration files and update them with your details.

#### API Keys Configuration

Rename `api-keys.sample.json` to `api-keys.json` and update with your Cloudflare API Token and Zone ID:

```json
{
  "ApiToken": "<CLOUDFLARE_API_TOKEN>",
  "ZoneId": "<ZONE_ID>"
}
```

#### Tunnel Credentials

Rename `credentials.sample.json` to `credentials.json`:

```json
{
  "AccountTag": "<ACCOUNT_TAG>",
  "TunnelSecret": "<TUNNEL_SECRET>",
  "TunnelID": "<TUNNEL_ID>"
}
```

#### Tunnel Configuration

Rename `config.sample.yml` to `config.yml`:

```yml
tunnel: <Tunnel ID>
credentials-file: ./credentials.json
ingress:
  - hostname: <Domain>
    service: http://localhost:<Port>
  - service: http_status:404
```

---

## Development Instructions

### Install Dependencies

Ensure you have Go installed. Then, run:

```bash
go mod tidy
```

### Start Cloudflare Tunnel

Run the app with the following command:

```bash
go run main.go --port=5173 --tunnel=my-tunnel --domain=anik.cc --credentials=./credentials.json --apiKeys=./api-keys.json
```

- --`port`: The local port to forward traffic.
- --`tunnel`: The name of the Cloudflare Tunnel.
- --`domain`: Your custom domain.
- --`credentials`: Path to the credentials file.
- --`apiKeys`: Path to the API keys file.

Sample Command:

```bash
go run main.go --port=5173 --tunnel=my-tunnel --domain=anik.cc --credentials=./credentials.json --apiKeys=./api-keys.json
```

File Structure:

```
.
├── main.go                # Application source code
├── api-keys.sample.json   # Sample API keys file
├── credentials.sample.json # Sample Tunnel credentials file
├── config.sample.yml      # Sample Tunnel configuration file
├── README.md              # Project README
```

---

## Troubleshooting

### Common Errors

1. Failed to create DNS record: An A, AAAA, or CNAME record already exists.

- A DNS record already exists for the domain. Ensure the record matches the tunnel.

2. Cloudflare authentication failed.

- Ensure cloudflared is installed and the API Token is valid.

3. Tunnel credentials not found.

- Verify that the credentials.json file exists and contains valid credentials.

## License

This project is licensed under the MIT License. See `LICENSE` for details.
