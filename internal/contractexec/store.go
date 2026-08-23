package contractexec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/patrickyoung/bench/internal/plyexec"
)

const draftFormat = "bench.contract-draft/v1"
const draftFormatV2 = "bench.contract-draft/v2"
const revisionFormat = "bench.contract-revision/v1"

type DraftStore interface {
	SaveDraft(Draft) (Draft, error)
	SaveDraftCAS(Draft, string) (Draft, error)
	MarkDraftRecorded(Draft) (Draft, error)
	Load() (Draft, string, error)
	PublishRevision(Draft, string) (Draft, error)
	MarkAdmitted(Draft) error
	DraftPath() string
}

// FileStore keeps mutable proposal bytes separate from immutable admitted
// revisions. Ambient mode places Dir in controller-namespaced workspace state;
// Cage deployments must place it outside every worker write root.
type FileStore struct{ Dir string }

type draftFile struct {
	Format                 string          `json:"format"`
	OutcomeID              string          `json:"outcome_id"`
	BaseRevisionID         string          `json:"base_revision_id,omitempty"`
	Generation             int             `json:"generation"`
	Intent                 string          `json:"intent"`
	Workspace              string          `json:"workspace"`
	Toolbox                string          `json:"toolbox,omitempty"`
	CompilerEvidenceSHA256 string          `json:"compiler_evidence_sha256"`
	Check                  string          `json:"check,omitempty"`
	CheckSHA256            string          `json:"check_sha256"`
	CheckAll               bool            `json:"check_all"`
	ApprovalPolicy         string          `json:"approval_policy,omitempty"`
	Skills                 []string        `json:"skills"`
	Contract               json.RawMessage `json:"contract"`
}

type revisionFile struct {
	Format           string          `json:"format"`
	OutcomeID        string          `json:"outcome_id"`
	ParentRevisionID string          `json:"parent_revision_id,omitempty"`
	ContractSHA256   string          `json:"contract_sha256"`
	Contract         json.RawMessage `json:"contract"`
}

type storeState struct {
	Format       string `json:"format"`
	Status       string `json:"status"`
	RevisionPath string `json:"revision_path,omitempty"`
	Draft
}

func (s FileStore) DraftPath() string { return filepath.Join(s.Dir, "draft.json") }

func (s FileStore) SaveDraft(draft Draft) (Draft, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return Draft{}, errors.New("contract store directory is empty")
	}
	if err := s.ensureDirs(); err != nil {
		return Draft{}, fmt.Errorf("create contract store: %w", err)
	}
	var saved Draft
	err := s.withLock(func() error {
		var saveErr error
		saved, saveErr = s.saveDraftUnlocked(draft)
		return saveErr
	})
	return saved, err
}

// SaveDraftCAS replaces the current proposal only when the caller still names
// the exact state it reviewed. An empty expected digest starts a new outcome
// only when no draft is already active.
func (s FileStore) SaveDraftCAS(draft Draft, expected string) (Draft, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return Draft{}, errors.New("contract store directory is empty")
	}
	if err := s.ensureDirs(); err != nil {
		return Draft{}, fmt.Errorf("create contract store: %w", err)
	}
	var saved Draft
	err := s.withLock(func() error {
		current, status, loadErr := s.loadUnlocked()
		if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
			return loadErr
		}
		if loadErr == nil {
			switch status {
			case "draft":
				if expected == "" || current.DraftSHA256 != expected {
					return errors.New("contract draft changed while revision was being prepared")
				}
			case "admitted":
				newOutcome := expected == "" && draft.ParentRevisionID == "" && draft.OutcomeID != current.OutcomeID
				amendment := expected == current.DraftSHA256 && draft.ParentRevisionID == current.RevisionID
				if !newOutcome && !amendment {
					return errors.New("admitted contract changed before amendment was prepared")
				}
			default:
				return errors.New("contract state is not editable")
			}
		} else if expected != "" {
			return errors.New("expected contract draft does not exist")
		}
		var saveErr error
		saved, saveErr = s.saveDraftUnlocked(draft)
		return saveErr
	})
	return saved, err
}

func (s FileStore) saveDraftUnlocked(draft Draft) (Draft, error) {
	draft, err := UpdateDraft(draft, string(draft.Contract))
	if err != nil {
		return Draft{}, err
	}
	draft.RecordedDraftSHA256 = ""
	format := draftFormat
	if approvalPolicy(draft.ApprovalPolicy) != plyexec.ApprovalOff {
		format = draftFormatV2
	}
	body, err := prettyJSON(draftFile{
		Format: format, OutcomeID: draft.OutcomeID, BaseRevisionID: draft.ParentRevisionID,
		Generation: draft.Generation, Intent: draft.Intent, Workspace: draft.Workspace, Toolbox: draft.Toolbox,
		CompilerEvidenceSHA256: draft.CompilerEvidenceSHA256, Check: draft.Check, CheckSHA256: draft.CheckSHA256,
		CheckAll: draft.CheckAll, ApprovalPolicy: policyForFile(draft.ApprovalPolicy), Skills: append([]string{}, draft.Skills...), Contract: draft.Contract,
	})
	if err != nil {
		return Draft{}, err
	}
	draft.DraftSHA256 = digestBytes(body)
	if err := atomicWrite(s.DraftPath(), body, 0o600); err != nil {
		return Draft{}, fmt.Errorf("write contract draft: %w", err)
	}
	if err := s.writeState("draft", draft, ""); err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func (s FileStore) Load() (Draft, string, error) {
	info, err := os.Lstat(s.Dir)
	if err != nil {
		return Draft{}, "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Draft{}, "", errors.New("contract store path is not a real directory")
	}
	var draft Draft
	var status string
	err = s.withLock(func() error {
		var loadErr error
		draft, status, loadErr = s.loadUnlocked()
		return loadErr
	})
	return draft, status, err
}

func (s FileStore) loadUnlocked() (Draft, string, error) {
	stateBytes, err := readRegular(filepath.Join(s.Dir, "state.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if recovered, _, recoverErr := s.readDraftFile(); recoverErr == nil {
				return recovered, "draft", nil
			}
		}
		return Draft{}, "", err
	}
	var state storeState
	if err := strictJSON(stateBytes, &state); err != nil || state.Format != "bench.contract-state/v1" {
		return Draft{}, "", errors.New("contract state is invalid")
	}
	draft := state.Draft
	if state.Status == "draft" {
		loaded, body, err := s.readDraftFile()
		if err != nil {
			return Draft{}, "", err
		}
		if sameDraftEnvelope(loaded, draft) {
			loaded.RecordedDraftSHA256 = draft.RecordedDraftSHA256
		}
		loaded.DraftSHA256 = digestBytes(body)
		return loaded, state.Status, nil
	}
	if state.Status != "admitted" {
		return Draft{}, "", errors.New("contract state status is invalid")
	}
	expectedPath := filepath.Join(s.Dir, "revisions", strings.TrimPrefix(draft.RevisionID, "sha256:")+".json")
	if filepath.Clean(state.RevisionPath) != filepath.Clean(expectedPath) {
		return Draft{}, "", errors.New("contract revision path escaped its store")
	}
	body, err := readRegular(state.RevisionPath)
	if err != nil {
		return Draft{}, "", err
	}
	var revision revisionFile
	if err := strictJSON(body, &revision); err != nil || revision.Format != revisionFormat {
		return Draft{}, "", errors.New("contract revision is invalid")
	}
	if digestBytes(body) != draft.RevisionID || revision.OutcomeID != draft.OutcomeID || revision.ParentRevisionID != draft.ParentRevisionID {
		return Draft{}, "", errors.New("contract revision identity does not match controller state")
	}
	revisionID, contractID := draft.RevisionID, draft.ContractID
	draft, err = UpdateDraft(draft, string(revision.Contract))
	if err != nil || draft.ContractSHA256 != revision.ContractSHA256 {
		return Draft{}, "", errors.New("contract revision body does not match its digest")
	}
	draft.RevisionID, draft.ContractID = revisionID, contractID
	if draft.ContractID != admissionID(draft) {
		return Draft{}, "", errors.New("contract admission identity does not match controller state")
	}
	if staged, _, stageErr := s.readDraftFile(); stageErr == nil {
		amendment := staged.ParentRevisionID == draft.RevisionID && staged.Generation > draft.Generation
		newOutcome := staged.ParentRevisionID == "" && staged.Generation == 1 && staged.OutcomeID != draft.OutcomeID
		if amendment || newOutcome {
			return staged, "draft", nil
		}
	}
	return draft, state.Status, nil
}

func (s FileStore) readDraftFile() (Draft, []byte, error) {
	body, err := readRegular(s.DraftPath())
	if err != nil {
		return Draft{}, nil, err
	}
	var file draftFile
	if err := strictJSON(body, &file); err != nil || file.Format != draftFormat && file.Format != draftFormatV2 {
		return Draft{}, nil, errors.New("contract draft is invalid")
	}
	if file.Format == draftFormat && file.ApprovalPolicy != "" || file.Format == draftFormatV2 && approvalPolicy(file.ApprovalPolicy) != plyexec.ApprovalEveryAction {
		return Draft{}, nil, errors.New("contract draft approval policy does not match its format")
	}
	_, canonical, contractDigest, err := Parse(string(file.Contract))
	if err != nil {
		return Draft{}, nil, err
	}
	return Draft{
		Schema: draftSchema(file.ApprovalPolicy), OutcomeID: file.OutcomeID, Generation: file.Generation, ParentRevisionID: file.BaseRevisionID,
		DraftSHA256: digestBytes(body), Intent: file.Intent, Workspace: file.Workspace, Toolbox: file.Toolbox,
		Contract: json.RawMessage(canonical), ContractSHA256: "sha256:" + contractDigest,
		CompilerEvidenceSHA256: file.CompilerEvidenceSHA256, Check: file.Check, CheckSHA256: file.CheckSHA256,
		CheckAll: file.CheckAll, ApprovalPolicy: policyForFile(file.ApprovalPolicy), Skills: append([]string{}, file.Skills...),
	}, body, nil
}

func sameDraftEnvelope(a, b Draft) bool {
	return a.OutcomeID == b.OutcomeID && a.Generation == b.Generation && a.ParentRevisionID == b.ParentRevisionID &&
		a.Intent == b.Intent && a.Workspace == b.Workspace && a.Toolbox == b.Toolbox &&
		a.ContractSHA256 == b.ContractSHA256 && a.CompilerEvidenceSHA256 == b.CompilerEvidenceSHA256 &&
		a.Check == b.Check && a.CheckSHA256 == b.CheckSHA256 && a.CheckAll == b.CheckAll && approvalPolicy(a.ApprovalPolicy) == approvalPolicy(b.ApprovalPolicy) && slices.Equal(a.Skills, b.Skills)
}

func (s FileStore) PublishRevision(draft Draft, expectedDraftSHA string) (Draft, error) {
	var published Draft
	err := s.withLock(func() error {
		var publishErr error
		published, publishErr = s.publishRevisionUnlocked(draft, expectedDraftSHA)
		return publishErr
	})
	return published, err
}

func (s FileStore) MarkDraftRecorded(draft Draft) (Draft, error) {
	var recorded Draft
	err := s.withLock(func() error {
		loaded, status, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if status != "draft" || loaded.DraftSHA256 != draft.DraftSHA256 || loaded.OutcomeID != draft.OutcomeID || loaded.Generation != draft.Generation {
			return errors.New("contract draft changed before its proposal record became durable")
		}
		loaded.RecordedDraftSHA256 = loaded.DraftSHA256
		if err := s.writeState("draft", loaded, ""); err != nil {
			return err
		}
		recorded = loaded
		return nil
	})
	return recorded, err
}

func (s FileStore) publishRevisionUnlocked(draft Draft, expectedDraftSHA string) (Draft, error) {
	loaded, status, err := s.loadUnlocked()
	if err != nil {
		return Draft{}, err
	}
	if status != "draft" {
		return Draft{}, errors.New("no editable contract draft is awaiting admission")
	}
	if loaded.DraftSHA256 != expectedDraftSHA {
		return Draft{}, fmt.Errorf("contract draft changed since review: expected %s, found %s", expectedDraftSHA, loaded.DraftSHA256)
	}
	if loaded.RecordedDraftSHA256 != loaded.DraftSHA256 {
		return Draft{}, errors.New("contract draft has not been sealed as a proposal")
	}
	if loaded.OutcomeID != draft.OutcomeID || loaded.Generation != draft.Generation {
		return Draft{}, errors.New("contract draft changed lineage before admission")
	}
	draft = loaded
	body, err := prettyJSON(revisionFile{
		Format: revisionFormat, OutcomeID: draft.OutcomeID, ParentRevisionID: draft.ParentRevisionID,
		ContractSHA256: draft.ContractSHA256, Contract: draft.Contract,
	})
	if err != nil {
		return Draft{}, err
	}
	draft.RevisionID = digestBytes(body)
	draft.ContractID = admissionID(draft)
	path := filepath.Join(s.Dir, "revisions", strings.TrimPrefix(draft.RevisionID, "sha256:")+".json")
	if existing, readErr := readRegular(path); readErr == nil {
		if !bytes.Equal(existing, body) {
			return Draft{}, errors.New("contract revision digest collision or corrupt existing object")
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return Draft{}, readErr
	} else if err := createExclusive(path, body); err != nil {
		return Draft{}, fmt.Errorf("publish contract revision: %w", err)
	}
	return draft, nil
}

func (s FileStore) MarkAdmitted(draft Draft) error {
	return s.withLock(func() error {
		loaded, status, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if status != "draft" || loaded.DraftSHA256 != draft.DraftSHA256 || loaded.RecordedDraftSHA256 != loaded.DraftSHA256 || loaded.OutcomeID != draft.OutcomeID || loaded.Generation != draft.Generation {
			return errors.New("contract draft changed before admission became durable")
		}
		path := filepath.Join(s.Dir, "revisions", strings.TrimPrefix(draft.RevisionID, "sha256:")+".json")
		return s.writeState("admitted", draft, path)
	})
}

func (s FileStore) withLock(action func() error) error {
	if err := s.ensureDirs(); err != nil {
		return err
	}
	lockPath := filepath.Join(s.Dir, "lock")
	if info, err := os.Lstat(lockPath); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return errors.New("contract lock is not a regular file")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	if lock == nil {
		_ = syscall.Close(fd)
		return errors.New("open contract lock")
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return action()
}

func (s FileStore) ensureDirs() error {
	for _, path := range []string{s.Dir, filepath.Join(s.Dir, "revisions")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("contract store path is not a real directory")
		}
	}
	return nil
}

func (s FileStore) writeState(status string, draft Draft, revisionPath string) error {
	body, err := prettyJSON(storeState{
		Format: "bench.contract-state/v1", Status: status, RevisionPath: revisionPath, Draft: draft,
	})
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(s.Dir, "state.json"), body, 0o600); err != nil {
		return fmt.Errorf("write contract state: %w", err)
	}
	return nil
}

func admissionID(draft Draft) string {
	if approvalPolicy(draft.ApprovalPolicy) == plyexec.ApprovalEveryAction {
		body, _ := json.Marshal(struct {
			Version   int      `json:"version"`
			Revision  string   `json:"revision_id"`
			Draft     string   `json:"draft_sha256"`
			Intent    string   `json:"intent_sha256"`
			Evidence  string   `json:"compiler_evidence_sha256"`
			Check     string   `json:"check_sha256"`
			CheckAll  bool     `json:"check_all"`
			Approval  string   `json:"approval_policy"`
			Workspace string   `json:"workspace"`
			Toolbox   string   `json:"toolbox,omitempty"`
			Skills    []string `json:"skills"`
			Method    string   `json:"method"`
		}{
			Version: 2, Revision: draft.RevisionID, Draft: draft.DraftSHA256, Intent: sha256Text(draft.Intent),
			Evidence: draft.CompilerEvidenceSHA256, Check: draft.CheckSHA256, CheckAll: draft.CheckAll,
			Approval: plyexec.ApprovalEveryAction, Workspace: draft.Workspace, Toolbox: draft.Toolbox,
			Skills: append([]string{}, draft.Skills...), Method: "interactive-user",
		})
		return digestBytes(body)
	}
	body, _ := json.Marshal(struct {
		Revision  string   `json:"revision_id"`
		Draft     string   `json:"draft_sha256"`
		Intent    string   `json:"intent_sha256"`
		Evidence  string   `json:"compiler_evidence_sha256"`
		Check     string   `json:"check_sha256"`
		CheckAll  bool     `json:"check_all"`
		Workspace string   `json:"workspace"`
		Toolbox   string   `json:"toolbox,omitempty"`
		Skills    []string `json:"skills"`
		Method    string   `json:"method"`
	}{
		Revision: draft.RevisionID, Draft: draft.DraftSHA256, Intent: sha256Text(draft.Intent),
		Evidence: draft.CompilerEvidenceSHA256, Check: draft.CheckSHA256, CheckAll: draft.CheckAll,
		Workspace: draft.Workspace, Toolbox: draft.Toolbox, Skills: append([]string{}, draft.Skills...), Method: "interactive-user",
	})
	return digestBytes(body)
}

func approvalPolicy(value string) string {
	if strings.TrimSpace(value) == "" {
		return plyexec.ApprovalOff
	}
	return strings.TrimSpace(value)
}

func policyForFile(value string) string {
	if approvalPolicy(value) == plyexec.ApprovalOff {
		return ""
	}
	return approvalPolicy(value)
}

func draftSchema(value string) int {
	if approvalPolicy(value) == plyexec.ApprovalEveryAction {
		return 2
	}
	return 1
}

func prettyJSON(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func strictJSON(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing data after JSON value")
	}
	return nil
}

func atomicWrite(path string, body []byte, mode fs.FileMode) error {
	if info, err := os.Lstat(filepath.Dir(path)); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("contract store parent is not a real directory")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".contract-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return syncDir(filepath.Dir(path))
}

func createExclusive(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return syncDir(filepath.Dir(path))
}

func readRegular(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("open contract path")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("contract path is not a regular file")
	}
	return io.ReadAll(file)
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func digestBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
