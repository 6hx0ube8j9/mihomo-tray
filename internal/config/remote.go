package config

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slog"
	"strconv"
	"strings"
	"time"
)

type FetchResult struct {
	TempPath string
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}


func (m *Manager) UpgradeSubscription(relPath string, proxyPort string, validator func(tmpPath string) error) (bool, error) {
	item, ok := m.GetProfileByPath(relPath)
	if !ok || item.URL == "" {
		return false, fmt.Errorf("找不到对应的远程订阅节点或 URL 为空")
	}

	fetchRes, err := m.FetchRemoteProfile(item.URL, proxyPort)
	if err != nil {
		return false, fmt.Errorf("拉取订阅失败: %w", err)
	}
	// defer os.Remove(fetchRes.TempPath)

	if err := validator(fetchRes.TempPath); err != nil {
		return false, err
	}

	item.Upload = fetchRes.Upload
	item.Download = fetchRes.Download
	item.Total = fetchRes.Total
	item.Expire = fetchRes.Expire
	item.LastUpdate = time.Now().Unix()

	if err := m.CommitRemoteProfile(fetchRes.TempPath, relPath, item); err != nil {
		return false, fmt.Errorf("订阅落盘失败: %w", err)
	}

	return true, nil
}

func (m *Manager) FetchRemoteProfile(subURL string, proxyPort string) (*FetchResult, error) {
	subURL = strings.TrimSpace(subURL)
	slog.Info("准备拉取远程订阅", "URL_LENGTH", len(subURL), "FULL_URL", subURL)

	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}

	if proxyPort != "" {
		if u, err := url.Parse("http://127.0.0.1:" + proxyPort); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
	}

	req, err := http.NewRequest("GET", subURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "clash-verge/v1.7.7 clash-meta")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Connection", "keep-alive")

	resp, err := client.Do(req)
	
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("远端服务器返回异常状态码: %d", resp.StatusCode)
	}

	cacheDir := filepath.Join(m.baseDir, ".cache")
	os.MkdirAll(cacheDir, 0755)
	tmpFile, err := os.CreateTemp(cacheDir, "sub_*.tmp")
	
	if err != nil {
		return nil, err
	}
	defer tmpFile.Close()

	limitReader := io.LimitReader(resp.Body, 15*1024*1024)
	if _, err := io.Copy(tmpFile, limitReader); err != nil {
		os.Remove(tmpFile.Name())
		return nil, err
	}

	res := &FetchResult{TempPath: tmpFile.Name()}

	if userInfo := resp.Header.Get("subscription-userinfo"); userInfo != "" {
		parts := strings.Split(userInfo, ";")
		for _, part := range parts {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 {
				val, _ := strconv.ParseInt(kv[1], 10, 64)
				switch strings.ToLower(kv[0]) {
				case "upload":
					res.Upload = val
				case "download":
					res.Download = val
				case "total":
					res.Total = val
				case "expire":
					res.Expire = val
				}
			}
		}
	}

	return res, nil
}


func (m *Manager) CommitRemoteProfile(tempPath string, targetRelPath string, item ProfileItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	targetAbs := filepath.Join(m.baseDir, filepath.FromSlash(targetRelPath))

	if err := os.Rename(tempPath, targetAbs); err != nil {
		return err
	}

	found := false
	for i, p := range m.data.Items {
		if p.Path == targetRelPath {
			m.data.Items[i] = item
			found = true
			break
		}
	}

	if !found {
		m.data.Items = append(m.data.Items, item)

		if len(m.data.Items) > 5 {
			m.data.Items = append(m.data.Items[:1], m.data.Items[2:]...)
		}
	}

	m.lockedSave()
	return nil
}

func (m *Manager) GetProfileByPath(relPath string) (ProfileItem, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.data.Items {
		if p.Path == relPath {
			return p, true
		}
	}
	return ProfileItem{}, false
}
