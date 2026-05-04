package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

type UserStorageRegistry struct {
	mu    sync.RWMutex
	path  string
	now   func() time.Time
	users map[int64]UserStorage
}

type UserStorage struct {
	UserID       int64     `json:"user_id"`
	ChatID       int64     `json:"chat_id"`
	RegisteredAt time.Time `json:"registered_at"`
}

type userStorageFile struct {
	Version int                    `json:"version"`
	Users   map[string]UserStorage `json:"users"`
}

func LoadUserStorageRegistry(path string) (*UserStorageRegistry, error) {
	registry := &UserStorageRegistry{
		path:  path,
		now:   time.Now,
		users: make(map[int64]UserStorage),
	}
	if path == "" {
		return registry, nil
	}

	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return registry, nil
		}
		return nil, fmt.Errorf("read user storage registry: %w", err)
	}
	if len(body) == 0 {
		return registry, nil
	}

	var stored userStorageFile
	if err := json.Unmarshal(body, &stored); err != nil {
		return nil, fmt.Errorf("decode user storage registry: %w", err)
	}
	for key, userStorage := range stored.Users {
		userID, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("decode user storage registry user id %q: %w", key, err)
		}
		if userStorage.UserID == 0 {
			userStorage.UserID = userID
		}
		if userStorage.ChatID == 0 {
			continue
		}
		registry.users[userID] = userStorage
	}
	return registry, nil
}

func (r *UserStorageRegistry) Register(userID int64, chatID int64) error {
	if r == nil {
		return fmt.Errorf("user storage registry is nil")
	}
	if userID == 0 {
		return fmt.Errorf("user id is required")
	}
	if chatID == 0 {
		return fmt.Errorf("chat id is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	previous, hadPrevious := r.users[userID]
	r.users[userID] = UserStorage{
		UserID:       userID,
		ChatID:       chatID,
		RegisteredAt: r.currentTime(),
	}
	if err := r.saveLocked(); err != nil {
		if hadPrevious {
			r.users[userID] = previous
		} else {
			delete(r.users, userID)
		}
		return err
	}
	return nil
}

func (r *UserStorageRegistry) Get(userID int64) (UserStorage, bool) {
	if r == nil {
		return UserStorage{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	userStorage, ok := r.users[userID]
	return userStorage, ok
}

func (r *UserStorageRegistry) currentTime() time.Time {
	if r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}

func (r *UserStorageRegistry) saveLocked() error {
	if r.path == "" {
		return nil
	}

	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create user storage registry dir: %w", err)
	}

	stored := userStorageFile{
		Version: 1,
		Users:   make(map[string]UserStorage, len(r.users)),
	}
	for userID, userStorage := range r.users {
		stored.Users[strconv.FormatInt(userID, 10)] = userStorage
	}

	tmp, err := os.CreateTemp(dir, ".reelsovoz-users-*.json")
	if err != nil {
		return fmt.Errorf("create user storage registry temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(stored); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode user storage registry: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod user storage registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close user storage registry temp file: %w", err)
	}
	if err := os.Rename(tmpName, r.path); err != nil {
		return fmt.Errorf("replace user storage registry: %w", err)
	}
	return nil
}
