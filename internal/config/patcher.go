package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

func processYAMLAST(content []byte, wantMode string, wantTun bool) ([]byte, map[string]string, bool, error) {
	extracted := make(map[string]string)
	var root yaml.Node

	if len(bytes.TrimSpace(content)) == 0 {
		root = yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{Kind: yaml.MappingNode},
			},
		}
	} else {
		if err := yaml.Unmarshal(content, &root); err != nil {
			return nil, nil, false, fmt.Errorf("YAML解析失败(请检查语法): %w", err)
		}
		if len(root.Content) == 0 {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.MappingNode})
		}
	}

	body := root.Content[0]
	if body.Kind != yaml.MappingNode {
		return nil, nil, false, fmt.Errorf("YAML 根节点必须是一个对象(Mapping)")
	}

	modified := false

	getVal := func(key string) string {
		for i := 0; i < len(body.Content); i += 2 {
			if body.Content[i].Value == key {
				return body.Content[i+1].Value
			}
		}
		return ""
	}

	ensureScalar := func(parent *yaml.Node, key, value string) {
		for i := 0; i < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				if parent.Content[i+1].Value != value {
					parent.Content[i+1].Value = value
					modified = true
				}
				return
			}
		}
		parent.Content = append(parent.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Value: value},
		)
		modified = true
	}

	ensureMapNode := func(parent *yaml.Node, key string) *yaml.Node {
		for i := 0; i < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				node := parent.Content[i+1]
				if node.Kind == yaml.AliasNode || node.Kind == yaml.ScalarNode {
					node.Kind = yaml.MappingNode
					node.Value = ""
					modified = true
				}
				return node
			}
		}
		newMap := &yaml.Node{Kind: yaml.MappingNode}
		parent.Content = append(parent.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: key},
			newMap,
		)
		modified = true
		return newMap
	}

	if port := getVal("mixed-port"); port != "" {
		extracted["port"] = port
	} else if port := getVal("port"); port != "" {
		extracted["port"] = port
	} else {
		ensureScalar(body, "mixed-port", DefaultMixedPort)
		extracted["port"] = DefaultMixedPort
	}

	ensureScalar(body, "socks-port", DefaultSocksPort)
	ensureScalar(body, "external-controller", DefaultExternalController)
	ensureScalar(body, "secret", DefaultSecret)
	ensureScalar(body, "external-ui", DefaultExternalUI)
	ensureScalar(body, "external-ui-url", DefaultExternalUIURL)

	currentMode := getVal("mode")
	if wantMode != "" {
		ensureScalar(body, "mode", wantMode)
	} else {
		if currentMode != "" {
			extracted["mode"] = currentMode
		} else {
			ensureScalar(body, "mode", DefaultMode)
			extracted["mode"] = DefaultMode
		}
	}

	tunNode := ensureMapNode(body, "tun")
	ensureScalar(tunNode, "enable", strconv.FormatBool(wantTun))
	ensureTunDefault := func(key, defaultVal string) {
		found := false
		for i := 0; i < len(tunNode.Content); i += 2 {
			if tunNode.Content[i].Value == key {
				found = true
				break
			}
		}
		if !found {
			ensureScalar(tunNode, key, defaultVal)
		}
	}
	ensureTunDefault("stack", DefaultTunStack)
	ensureTunDefault("auto-route", strconv.FormatBool(DefaultTunAutoRoute))
	ensureTunDefault("device", DefaultTunDevice)

	for i := 0; i < len(tunNode.Content); i += 2 {
		if tunNode.Content[i].Value == "device" {
			extracted["tun_device"] = tunNode.Content[i+1].Value
			break
		}
	}

	if !modified {
		return content, extracted, false, nil
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&root); err != nil {
		return nil, nil, false, err
	}
	encoder.Close()

	return buf.Bytes(), extracted, true, nil
}

func writeTmpAndRename(baseDir, targetPath string, content []byte) error {
	tmpFile, err := os.CreateTemp(baseDir, "config.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()

	cleaned := false
	defer func() {
		if !cleaned {
			_ = tmpFile.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	cleaned = true

	return os.Rename(tmpName, targetPath)
}
