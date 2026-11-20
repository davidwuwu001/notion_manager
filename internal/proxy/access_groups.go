package proxy

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var groupNamePattern = regexp.MustCompile(`^[\pL\pN_. -]{1,64}$`)

type AccessGroup struct {
	Name       string   `json:"name"`
	AccountIDs []string `json:"account_ids"`
}
type AccessKey struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Group string `json:"group"`
	Hash  string `json:"hash"`
}
type CreatedAccessKey struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}
type accessState struct {
	Groups map[string]AccessGroup `json:"groups"`
	Keys   map[string]AccessKey   `json:"keys"`
}
type AccessStore struct {
	mu            sync.RWMutex
	path          string
	state         accessState
	watches       map[string]map[uint64]context.CancelFunc
	watchSequence uint64
}
type AccessScope struct {
	Allowed  map[string]bool
	Identity string
}
type accessScopeKey struct{}

func WithAccessScope(ctx context.Context, scope *AccessScope) context.Context {
	return context.WithValue(ctx, accessScopeKey{}, scope)
}
func AccessScopeFromContext(ctx context.Context) *AccessScope {
	scope, _ := ctx.Value(accessScopeKey{}).(*AccessScope)
	return scope
}

func NewAccessStore(path string) (*AccessStore, error) {
	s := &AccessStore{path: path, state: accessState{Groups: map[string]AccessGroup{}, Keys: map[string]AccessKey{}}}
	if path == "" {
		return s, nil
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return nil, errors.New("access file must be private and regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&s.state); err != nil {
		return nil, err
	}
	var trailing any
	if err = dec.Decode(&trailing); err != io.EOF {
		return nil, errors.New("invalid access store trailing data")
	}
	if s.state.Groups == nil || s.state.Keys == nil {
		return nil, errors.New("invalid access store")
	}
	for name, g := range s.state.Groups {
		if name != g.Name || validateGroup(name, g.AccountIDs) != nil {
			return nil, errors.New("invalid access group")
		}
	}
	hashes := map[string]bool{}
	for id, k := range s.state.Keys {
		_, groupExists := s.state.Groups[k.Group]
		if id != k.ID || len(k.Hash) != 64 || len(k.ID) != 32 || !groupExists || !groupNamePattern.MatchString(k.Name) || strings.TrimSpace(k.Name) != k.Name {
			return nil, errors.New("invalid access key")
		}
		if _, err := hex.DecodeString(k.ID); err != nil {
			return nil, err
		}
		if hashes[k.Hash] {
			return nil, errors.New("duplicate access key hash")
		}
		hashes[k.Hash] = true
		if _, err := hex.DecodeString(k.Hash); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func validateGroup(name string, ids []string) error {
	if !groupNamePattern.MatchString(name) || strings.TrimSpace(name) != name {
		return errors.New("invalid group name")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !isAccountID(id) || seen[id] {
			return errors.New("invalid account ids")
		}
		seen[id] = true
	}
	return nil
}
func cloneAccessState(in accessState) accessState {
	out := accessState{Groups: make(map[string]AccessGroup, len(in.Groups)), Keys: make(map[string]AccessKey, len(in.Keys))}
	for name, g := range in.Groups {
		g.AccountIDs = append([]string{}, g.AccountIDs...)
		out.Groups[name] = g
	}
	for id, k := range in.Keys {
		out.Keys[id] = k
	}
	return out
}
func (s *AccessStore) save(next accessState) error {
	if s.path == "" {
		s.state = next
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := ensurePrivateAccountsDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".access-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		err = json.NewEncoder(f).Encode(next)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.state = next
	return nil
}
func (s *AccessStore) SetGroup(name string, ids []string) error {
	if err := validateGroup(name, ids); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneAccessState(s.state)
	next.Groups[name] = AccessGroup{Name: name, AccountIDs: append([]string{}, ids...)}
	if err := s.save(next); err != nil {
		return err
	}
	for id, k := range s.state.Keys {
		if k.Group == name {
			s.cancelKeyLocked(id)
		}
	}
	return nil
}
func (s *AccessStore) DeleteGroup(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneAccessState(s.state)
	if _, ok := next.Groups[name]; !ok {
		return errors.New("group not found")
	}
	delete(next.Groups, name)
	var revoked []string
	for id, k := range next.Keys {
		if k.Group == name {
			revoked = append(revoked, id)
			delete(next.Keys, id)
		}
	}
	if err := s.save(next); err != nil {
		return err
	}
	for _, id := range revoked {
		s.cancelKeyLocked(id)
	}
	return nil
}
func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *AccessStore) CreateKey(name, group string) (CreatedAccessKey, error) {
	if !groupNamePattern.MatchString(name) || strings.TrimSpace(name) != name {
		return CreatedAccessKey{}, errors.New("invalid key name")
	}
	id, err := randomHex(16)
	if err != nil {
		return CreatedAccessKey{}, err
	}
	token, err := randomHex(32)
	if err != nil {
		return CreatedAccessKey{}, err
	}
	secret := "sk-nm-" + token
	digest := sha256.Sum256([]byte(secret))
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Groups[group]; !ok {
		return CreatedAccessKey{}, errors.New("group not found")
	}
	next := cloneAccessState(s.state)
	next.Keys[id] = AccessKey{ID: id, Name: name, Group: group, Hash: hex.EncodeToString(digest[:])}
	if err = s.save(next); err != nil {
		return CreatedAccessKey{}, err
	}
	return CreatedAccessKey{ID: id, Secret: secret}, nil
}
func (s *AccessStore) UpdateKey(id, name, group string) error {
	if !groupNamePattern.MatchString(name) || strings.TrimSpace(name) != name {
		return errors.New("invalid key name")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneAccessState(s.state)
	k, ok := next.Keys[id]
	if !ok {
		return errors.New("key not found")
	}
	if _, ok = next.Groups[group]; !ok {
		return errors.New("group not found")
	}
	k.Name = name
	k.Group = group
	next.Keys[id] = k
	if err := s.save(next); err != nil {
		return err
	}
	s.cancelKeyLocked(id)
	return nil
}
func (s *AccessStore) DeleteKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneAccessState(s.state)
	if _, ok := next.Keys[id]; !ok {
		return errors.New("key not found")
	}
	delete(next.Keys, id)
	if err := s.save(next); err != nil {
		return err
	}
	s.cancelKeyLocked(id)
	return nil
}
func (s *AccessStore) Authorize(secret string) (*AccessScope, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, scope, ok := s.authorizeLocked(secret)
	return scope, ok
}
func (s *AccessStore) authorizeLocked(secret string) (string, *AccessScope, bool) {
	d := sha256.Sum256([]byte(secret))
	hash := hex.EncodeToString(d[:])
	for _, k := range s.state.Keys {
		if k.Hash == hash {
			g, ok := s.state.Groups[k.Group]
			if !ok {
				return "", nil, false
			}
			allowed := make(map[string]bool, len(g.AccountIDs))
			for _, id := range g.AccountIDs {
				allowed[id] = true
			}
			return k.ID, &AccessScope{Allowed: allowed, Identity: k.ID + ":" + k.Group}, true
		}
	}
	return "", nil, false
}
func (s *AccessStore) cancelKeyLocked(id string) {
	for _, cancel := range s.watches[id] {
		cancel()
	}
	delete(s.watches, id)
}

// AuthorizeRequest registers the request while holding the policy lock, so revocation
// cannot race between authorization and cancellation registration.
func (s *AccessStore) AuthorizeRequest(parent context.Context, secret string) (*AccessScope, context.Context, func(), bool) {
	s.mu.Lock()
	id, scope, ok := s.authorizeLocked(secret)
	if !ok {
		s.mu.Unlock()
		return nil, nil, nil, false
	}
	ctx, cancel := context.WithCancel(parent)
	if s.watches == nil {
		s.watches = map[string]map[uint64]context.CancelFunc{}
	}
	if s.watches[id] == nil {
		s.watches[id] = map[uint64]context.CancelFunc{}
	}
	s.watchSequence++
	n := s.watchSequence
	s.watches[id][n] = cancel
	s.mu.Unlock()
	return scope, ctx, func() {
		cancel()
		s.mu.Lock()
		if entries := s.watches[id]; entries != nil {
			delete(entries, n)
			if len(entries) == 0 {
				delete(s.watches, id)
			}
		}
		s.mu.Unlock()
	}, true
}
func (s *AccessStore) Snapshot() (groups []AccessGroup, keys []map[string]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groups = []AccessGroup{}
	keys = []map[string]string{}
	for _, g := range s.state.Groups {
		g.AccountIDs = append([]string{}, g.AccountIDs...)
		groups = append(groups, g)
	}
	for _, k := range s.state.Keys {
		keys = append(keys, map[string]string{"id": k.ID, "name": k.Name, "group": k.Group})
	}
	return
}
func ScopedFingerprint(fingerprint string, scope *AccessScope) string {
	if scope == nil {
		return fingerprint
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s", scope.Identity, fingerprint)))
	return hex.EncodeToString(sum[:16])
}

// ScopedModels only exposes model entries discovered for permitted, currently
// eligible accounts. Unknown model sets stay unknown; the global fallback is
// deliberately not exposed to a restricted key.
func (p *AccountPool) ScopedModels(allowed map[string]bool) []ModelEntry {
	accounts := p.AccountsSnapshot()
	p.routingMu.Lock()
	defer p.routingMu.Unlock()
	seen := map[string]bool{}
	models := []ModelEntry{}
	for _, acc := range accounts {
		acc.EnsureAccountID()
		if !allowed[acc.AccountID] {
			continue
		}
		status := p.routingAccountStatusLocked(acc, p.routingConfigLocked(), time.Now())
		if status.Status != "available" && status.Status != "busy" {
			continue
		}
		for _, m := range acc.modelsSnapshot() {
			if !seen[m.ID] && routingSupportsModel(acc, m.ID, false, p.routingStateLocked(acc)) {
				seen[m.ID] = true
				models = append(models, m)
			}
		}
	}
	if len(models) > 0 && !seen[autoModelID] {
		models = append(models, ModelEntry{Name: "Auto", ID: autoModelID})
	}
	return models
}
