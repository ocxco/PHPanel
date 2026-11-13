package panel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

func validSignatureMetadata(a Artifact) bool {
	if a.Component != a.SignatureAuthority || !fullVersionPattern.MatchString(a.Version) {
		return false
	}
	u, err := url.Parse(a.SignatureURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Port() != "" {
		return false
	}
	switch a.Component {
	case "nginx":
		return u.Hostname() == "nginx.org" && u.Path == "/download/nginx-"+a.Version+".zip.asc"
	case "mysql":
		return u.Hostname() == "cdn.mysql.com" && strings.HasPrefix(u.Path, "/Downloads/MySQL-") && path.Base(u.Path) == "mysql-"+a.Version+"-winx64.zip.asc"
	}
	return false
}

var mysqlKeyPattern = regexp.MustCompile(`(?i)href\s*=\s*["'](RPM-GPG-KEY-mysql(?:-[0-9]{4})?)["']`)
var nginxKeyPattern = regexp.MustCompile(`(?i)href\s*=\s*["'](/keys/[a-zA-Z0-9_-]+\.key)["']`)

func officialKeyAddresses(authority string, body []byte) []string {
	seen := map[string]bool{}
	addresses := []string{}
	var matches [][]string
	prefix := ""
	switch authority {
	case "mysql":
		matches = mysqlKeyPattern.FindAllStringSubmatch(string(body), 100)
		prefix = "https://repo.mysql.com/"
	case "nginx":
		matches = nginxKeyPattern.FindAllStringSubmatch(string(body), 100)
		prefix = "https://nginx.org"
	}
	for _, match := range matches {
		address := prefix + match[1]
		if !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(addresses)))
	if len(addresses) > 8 {
		addresses = addresses[:8]
	}
	return addresses
}
func parseKeyRing(data []byte) (openpgp.EntityList, error) {
	return openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
}
func (m *Manager) officialKeys(ctx context.Context, client *http.Client, authority string) (openpgp.EntityList, error) {
	cache := filepath.Join(m.root, "state", "catalog-keys", authority+".asc")
	cached, _ := os.ReadFile(cache)
	index := "https://repo.mysql.com/"
	if authority == "nginx" {
		index = "https://nginx.org/en/pgp_keys.html"
	}
	body, err := catalogueBody(ctx, client, index, 2<<20)
	if err != nil {
		if len(cached) > 0 {
			return parseKeyRing(cached)
		}
		return nil, fmt.Errorf("读取官方签名公钥目录失败: %w", err)
	}
	addresses := officialKeyAddresses(authority, body)
	if len(addresses) == 0 {
		if len(cached) > 0 {
			return parseKeyRing(cached)
		}
		return nil, fmt.Errorf("官方页面没有可用的签名公钥")
	}
	ring := openpgp.EntityList{}
	seen := map[string]bool{}
	combined := bytes.Buffer{}
	for _, address := range addresses {
		data, err := catalogueBody(ctx, client, address, 1<<20)
		if err != nil {
			continue
		}
		entities, err := parseKeyRing(data)
		if err != nil {
			continue
		}
		// Prefer renewed certificates over expired copies of the same key.
		for _, entity := range entities {
			fingerprint := fmt.Sprintf("%x", entity.PrimaryKey.Fingerprint)
			if !seen[fingerprint] {
				seen[fingerprint] = true
				ring = append(ring, entity)
			}
		}
	}
	if len(ring) == 0 {
		if len(cached) > 0 {
			return parseKeyRing(cached)
		}
		return nil, fmt.Errorf("没有可验证的官方签名公钥")
	}
	writer, encodeErr := armor.Encode(&combined, openpgp.PublicKeyType, nil)
	if encodeErr == nil {
		for _, entity := range ring {
			if encodeErr = entity.Serialize(writer); encodeErr != nil {
				break
			}
		}
		closeErr := writer.Close()
		if encodeErr == nil {
			encodeErr = closeErr
		}
	}
	if err := os.MkdirAll(filepath.Dir(cache), 0700); err == nil {
		tmp := cache + ".tmp"
		if encodeErr == nil && os.WriteFile(tmp, combined.Bytes(), 0600) == nil {
			_ = os.Rename(tmp, cache)
		}
	}
	return ring, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
func checkSignature(ctx context.Context, ring openpgp.EntityList, archive string, signature []byte) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = openpgp.CheckArmoredDetachedSignature(ring, contextReader{ctx: ctx, reader: file}, bytes.NewReader(signature), nil)
	if err != nil {
		return fmt.Errorf("官方 OpenPGP 签名验证失败，拒绝安装: %w", err)
	}
	return nil
}
func (m *Manager) verifyArtifactSignature(ctx context.Context, client *http.Client, a Artifact, archive string) error {
	if !validSignatureMetadata(a) {
		return fmt.Errorf("不支持此安装包的签名来源")
	}
	signature, err := catalogueBody(ctx, client, a.SignatureURL, 256<<10)
	if err != nil && a.Component == "mysql" {
		branch := strings.Join(strings.Split(a.Version, ".")[:2], ".")
		signature, err = catalogueBody(ctx, client, "https://cdn.mysql.com/archives/mysql-"+branch+"/mysql-"+a.Version+"-winx64.zip.asc", 256<<10)
	}
	if err != nil {
		return fmt.Errorf("读取官方签名失败: %w", err)
	}
	ring, err := m.officialKeys(ctx, client, a.SignatureAuthority)
	if err != nil {
		return err
	}
	return checkSignature(ctx, ring, archive, signature)
}
func (m *Manager) rememberVerifiedArtifact(a Artifact, hash string) error {
	return m.change(func(state *State) error {
		for n := range state.Artifacts {
			if state.Artifacts[n].ID == a.ID {
				old := &state.Artifacts[n]
				if validHash(old.SHA256) && old.SHA256 != hash {
					return fmt.Errorf("目录摘要发生冲突，拒绝覆盖")
				}
				old.SHA256 = hash
				old.Verification = "官方 OpenPGP 签名 + SHA-256"
				return nil
			}
		}
		return fmt.Errorf("签名验证后目录条目不存在")
	})
}
