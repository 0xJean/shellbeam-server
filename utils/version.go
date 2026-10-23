package utils

const (
	ProductName = "ShellBeam Server"
	// UpstreamVersion is the API and plugin compatibility baseline, not the
	// ShellBeam release version. Existing clients require this exact value.
	UpstreamVersion = "1.6.0"
	UpstreamCommit  = "1b76b7ad5bc3de6529bb7f0577069b4ac241fec3"
)

var (
	CurrentVersion = UpstreamVersion
	ServerVersion  = "0.1.0-dev"
	VersionHash    = "unknown"
)

// VersionInfo keeps the legacy version/hash contract and adds fork provenance.
type VersionInfo struct {
	Version         string `json:"version"`
	Hash            string `json:"hash"`
	Product         string `json:"product"`
	ProductVersion  string `json:"product_version"`
	UpstreamVersion string `json:"upstream_version"`
	UpstreamCommit  string `json:"upstream_commit"`
}

func GetVersionInfo() VersionInfo {
	return VersionInfo{
		Version:         CurrentVersion,
		Hash:            VersionHash,
		Product:         ProductName,
		ProductVersion:  ServerVersion,
		UpstreamVersion: UpstreamVersion,
		UpstreamCommit:  UpstreamCommit,
	}
}
