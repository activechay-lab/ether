package clusterconfig

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
)

// Deployment contains paths and node-local policy only. Secret CONTENTS must
// reside in separate mounted files. This document is never a shared setting.
type Deployment struct {
	EncoderRPCDeadlineMillis  int64            `json:"encoderRPCDeadlineMillis,omitempty"`
	EncoderFailureGraceMillis int64            `json:"encoderFailureGraceMillis,omitempty"`
	EncoderSegmentWaitMillis  int64            `json:"encoderSegmentWaitMillis,omitempty"`
	LedgerCapacity            int              `json:"ledgerCapacity,omitempty"`
	TempDir                   string           `json:"tempDir,omitempty"`
	CacheDir                  string           `json:"cacheDir,omitempty"`
	DrainSeconds              int              `json:"drainSeconds,omitempty"`
	Capacity                  int              `json:"capacity"`
	ClockSkewMillis           int64            `json:"clockSkewMillis"`
	ClockBoundKnown           bool             `json:"clockBoundKnown"`
	ResourceScope             string           `json:"resourceScope,omitempty"`
	Bootstrap                 bool             `json:"bootstrap"`
	Roles                     config.NodeRoles `json:"roles"`
	InternalAddress           string           `json:"internalAddress"`
	PrivateListen             string           `json:"privateListen"`
	ProbeListen               string           `json:"probeListen"`
	DatabaseURLFile           string           `json:"databaseURLFile"`
	ClusterKeyFile            string           `json:"clusterKeyFile"`
	CAFile                    string           `json:"caFile"`
	CAKeyFile                 string           `json:"caKeyFile"`
	RootsFile                 string           `json:"rootsFile"`
	CgroupV2Path              string           `json:"cgroupV2Path,omitempty"`
}

func ReadDeployment(path string) (Deployment, error) {
	var d Deployment
	f, err := os.Open(path)
	if err != nil {
		return d, errors.New("deployment: cannot open settings file")
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil {
		return Deployment{}, errors.New("deployment: invalid settings document")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return Deployment{}, errors.New("deployment: trailing settings data")
	}
	if d.PrivateListen == "" {
		d.PrivateListen = ":32401"
	}
	if d.ProbeListen == "" {
		d.ProbeListen = ":32402"
	}
	for _, address := range []string{d.PrivateListen, d.ProbeListen} {
		_, port, err := net.SplitHostPort(address)
		n, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil || n < 1 || n > 65535 {
			return Deployment{}, errors.New("deployment: invalid listener address")
		}
	}
	for _, p := range []string{d.TempDir, d.CacheDir, d.CgroupV2Path, d.DatabaseURLFile, d.ClusterKeyFile, d.CAFile, d.CAKeyFile, d.RootsFile} {
		if p != "" && !filepath.IsAbs(p) {
			return Deployment{}, errors.New("deployment: paths must be absolute")
		}
	}
	if d.PrivateListen == d.ProbeListen || d.InternalAddress == "" || d.DatabaseURLFile == "" || d.ClusterKeyFile == "" || d.CAFile == "" || d.CAKeyFile == "" || d.RootsFile == "" {
		return Deployment{}, errors.New("deployment: missing or conflicting required settings")
	}
	if d.LedgerCapacity == 0 {
		d.LedgerCapacity = 1000000
	}
	if d.LedgerCapacity < 4096 || d.LedgerCapacity > 10000000 || d.LedgerCapacity < d.Capacity {
		return Deployment{}, errors.New("deployment: ledgerCapacity must be 4096..10000000 and cover concurrent/history capacity")
	}
	if d.DrainSeconds == 0 {
		d.DrainSeconds = 120
	}
	if d.DrainSeconds < 1 || d.DrainSeconds > 3600 {
		return Deployment{}, errors.New("deployment: drainSeconds must be 1..3600")
	}
	if d.EncoderRPCDeadlineMillis < 0 || d.EncoderRPCDeadlineMillis > 5000 || d.EncoderFailureGraceMillis < 0 || d.EncoderFailureGraceMillis > 60000 || d.EncoderSegmentWaitMillis < 0 || d.EncoderSegmentWaitMillis > 60000 {
		return d, errors.New("invalid encoder recovery deadlines")
	}
	if d.Capacity < 1 || d.Capacity > 4096 || !d.ClockBoundKnown || d.ClockSkewMillis < 0 || d.ClockSkewMillis > 60000 || len(d.ResourceScope) > 200 {
		return Deployment{}, errors.New("deployment: explicit bounded capacity and known clock skew required")
	}
	if err := d.Roles.Validate(); err != nil {
		return Deployment{}, err
	}
	if err := config.ValidateInternalAddress(d.InternalAddress); err != nil {
		return Deployment{}, err
	}
	return d, nil
}

// ReadSecretFile bounds input and deliberately omits filenames and bytes from
// errors. Each call reopens the path so Kubernetes projected-volume swaps work.
func ReadSecretFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("deployment: cannot read required Secret file")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(b) == 0 || int64(len(b)) > limit {
		return nil, errors.New("deployment: invalid Secret file")
	}
	return b, nil
}

func (d Deployment) ProvisionOptions(dataDir string) (ProvisionOptions, error) {
	url, err := ReadSecretFile(d.DatabaseURLFile, 16384)
	if err != nil {
		return ProvisionOptions{}, err
	}
	key, err := ReadSecretFile(d.ClusterKeyFile, 32)
	if err != nil {
		return ProvisionOptions{}, err
	}
	return ProvisionOptions{DataDir: dataDir, TempDir: d.TempDir, CacheDir: d.CacheDir, DatabaseURL: strings.TrimSpace(string(url)), Key: key, Bootstrap: d.Bootstrap, Roles: d.Roles, InternalAddress: d.InternalAddress}, nil
}
