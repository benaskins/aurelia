package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

var certCmd = &cobra.Command{
	Use:   "cert",
	Short: "Manage TLS certificates",
}

var certRenewCmd = &cobra.Command{
	Use:   "renew",
	Short: "Renew the wildcard TLS certificate",
	Long: `Issue a new *.hestia.internal wildcard certificate from the PKI
secrets engine on OpenBao, write it to the data directory, and
reload Traefik to pick up the new certificate.`,
	RunE: runCertRenew,
}

var certBundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Output the CA certificate chain for browser import",
	Long: `Print the CA chain PEM to stdout. Pipe to a file and import
into your browser's certificate store to trust *.hestia.internal.

  aurelia cert bundle > lamina-ca.crt`,
	RunE: runCertBundle,
}

func init() {
	certRenewCmd.Flags().String("ttl", "720h", "Certificate time to live")
	certRenewCmd.Flags().String("mount", "pki_lamina", "PKI secrets engine mount")
	certRenewCmd.Flags().String("role", "server", "PKI role to issue against")
	certRenewCmd.Flags().String("cn", "*.hestia.internal", "Common name for the certificate")
	certRenewCmd.Flags().String("vault-token", "", "OpenBao root token (reads from AURELIA_ROOT/.vault-keys if not set)")
	certCmd.AddCommand(certRenewCmd)
	certCmd.AddCommand(certBundleCmd)
	rootCmd.AddCommand(certCmd)
}

func runCertRenew(cmd *cobra.Command, _ []string) error {
	ttl, _ := cmd.Flags().GetString("ttl")
	mount, _ := cmd.Flags().GetString("mount")
	role, _ := cmd.Flags().GetString("role")
	cn, _ := cmd.Flags().GetString("cn")
	jsonOut, _ := cmd.Flags().GetBool("json")

	certDir, err := wildcardCertDir()
	if err != nil {
		return err
	}

	vaultToken, _ := cmd.Flags().GetString("vault-token")

	fmt.Printf("Issuing %s (mount=%s, role=%s, ttl=%s)...\n", cn, mount, role, ttl)

	issuer, err := resolvePKICertIssuer(mount, vaultToken)
	if err != nil {
		return fmt.Errorf("resolving PKI backend: %w", err)
	}

	cert, err := issuer.Issue(role, cn, ttl)
	if err != nil {
		return fmt.Errorf("issuing certificate: %w", err)
	}

	if err := os.MkdirAll(certDir, 0755); err != nil {
		return fmt.Errorf("creating cert dir: %w", err)
	}

	if err := os.WriteFile(filepath.Join(certDir, "cert.crt"), []byte(cert.Certificate), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "cert.key"), []byte(cert.PrivateKey), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, "ca-chain.crt"), []byte(cert.CAChain), 0644); err != nil {
		return err
	}
	fullchain := cert.Certificate + "\n" + cert.CAChain
	if err := os.WriteFile(filepath.Join(certDir, "fullchain.crt"), []byte(fullchain), 0644); err != nil {
		return err
	}

	expiry := time.Unix(cert.Expiration, 0)

	if jsonOut {
		return printJSON(map[string]any{
			"common_name": cn,
			"serial":      cert.Serial,
			"expires":     expiry.Format(time.RFC3339),
			"cert_dir":    certDir,
		})
	}

	fmt.Printf("Certificate issued: %s\n", cn)
	fmt.Printf("  Serial:  %s\n", cert.Serial)
	fmt.Printf("  Expires: %s\n", expiry.Format(time.RFC3339))
	fmt.Printf("  Dir:     %s\n", certDir)

	// Reload traefik to pick up the new cert
	fmt.Print("Reloading traefik...")
	if _, err := apiPost("/v1/services/infra-traefik/restart"); err != nil {
		fmt.Printf(" failed: %v\n", err)
		fmt.Println("Restart traefik manually: aurelia restart infra-traefik")
	} else {
		fmt.Println(" done")
	}

	return nil
}

func runCertBundle(cmd *cobra.Command, _ []string) error {
	certDir, err := wildcardCertDir()
	if err != nil {
		return err
	}

	caChain, err := os.ReadFile(filepath.Join(certDir, "ca-chain.crt"))
	if err != nil {
		return fmt.Errorf("reading CA chain: %w (run 'aurelia cert renew' first)", err)
	}

	fmt.Print(string(caChain))
	return nil
}

func wildcardCertDir() (string, error) {
	root := os.Getenv("AURELIA_ROOT")
	if root == "" {
		return "", fmt.Errorf("AURELIA_ROOT is not set")
	}
	return filepath.Join(root, "data", "vault", "server-certs", "wildcard"), nil
}
