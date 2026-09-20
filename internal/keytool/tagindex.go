package keytool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// KeyTag 一把密钥的标签与备注，按指纹索引。
type KeyTag struct {
	Fingerprint string    `json:"fingerprint"`
	Path        string    `json:"path,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// TagIndex 持久化的密钥标签索引（与公钥文件解耦：改文件名/位置不影响标签）。
type TagIndex struct {
	Keys []KeyTag `json:"keys"`
}

// DefaultTagIndexPath 返回标签索引的默认路径：~/.sshtool/key-tags.json。
func DefaultTagIndexPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".sshtool", "key-tags.json")
	}
	return filepath.Join(home, ".sshtool", "key-tags.json")
}

// LoadTagIndex 读取索引；文件不存在时返回空索引（不报错）。
func LoadTagIndex(path string) (*TagIndex, error) {
	if path == "" {
		path = DefaultTagIndexPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &TagIndex{}, nil
		}
		return nil, err
	}
	ix := &TagIndex{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, ix); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

// Save 原子写入索引（先写临时文件再改名，避免写一半损坏）。
func (ix *TagIndex) Save(path string) error {
	if path == "" {
		path = DefaultTagIndexPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ix, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Get 按指纹取一条记录。
func (ix *TagIndex) Get(fp string) (KeyTag, bool) {
	for _, k := range ix.Keys {
		if k.Fingerprint == fp {
			return k, true
		}
	}
	return KeyTag{}, false
}

// TagsOf 返回某指纹的标签（不存在时为空）。
func (ix *TagIndex) TagsOf(fp string) []string {
	if k, ok := ix.Get(fp); ok {
		return k.Tags
	}
	return nil
}

// Set 新增或更新一条记录（标签会被规范化）。
func (ix *TagIndex) Set(fp, path string, tags []string, notes string) {
	tags = NormalizeTags(tags)
	for i := range ix.Keys {
		if ix.Keys[i].Fingerprint == fp {
			ix.Keys[i].Path = path
			ix.Keys[i].Tags = tags
			ix.Keys[i].Notes = notes
			ix.Keys[i].UpdatedAt = time.Now()
			return
		}
	}
	ix.Keys = append(ix.Keys, KeyTag{
		Fingerprint: fp,
		Path:        path,
		Tags:        tags,
		Notes:       notes,
		UpdatedAt:   time.Now(),
	})
}

// Remove 删除某指纹的记录（例如密钥被移除后清理）。
func (ix *TagIndex) Remove(fp string) {
	out := ix.Keys[:0]
	for _, k := range ix.Keys {
		if k.Fingerprint != fp {
			out = append(out, k)
		}
	}
	ix.Keys = out
}

// Prune 清理索引中已不存在于 keys 里的指纹（keys 为当前本机密钥）。
func (ix *TagIndex) Prune(keys []KeyInfo) {
	alive := map[string]bool{}
	for _, k := range keys {
		alive[k.Fingerprint] = true
	}
	out := ix.Keys[:0]
	for _, k := range ix.Keys {
		if alive[k.Fingerprint] {
			out = append(out, k)
		}
	}
	ix.Keys = out
}