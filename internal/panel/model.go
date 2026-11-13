package panel

type Source struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type Artifact struct {
	ID                 string   `json:"id"`
	Component          string   `json:"component"`
	Version            string   `json:"version"`
	Build              string   `json:"build"`
	Publisher          string   `json:"publisher"`
	SHA256             string   `json:"sha256"`
	Verification       string   `json:"verification"`
	Sources            []Source `json:"sources"`
	SignatureURL       string   `json:"signatureUrl,omitempty"`
	SignatureAuthority string   `json:"signatureAuthority,omitempty"`
}
type InstalledPackage struct {
	DirectoryName string `json:"directoryName,omitempty"`
	ID            string `json:"id"`
	Component     string `json:"component"`
	Version       string `json:"version"`
	Publisher     string `json:"publisher"`
	SHA256        string `json:"sha256"`
	Verification  string `json:"verification"`
	Path          string `json:"path"`
	Executable    string `json:"executable"`
	InstalledAt   string `json:"installedAt"`
}
type Instance struct {
	NginxAutoPHP     *bool  `json:"nginxAutoPHP,omitempty"`
	NginxStopPHP     bool   `json:"nginxStopPHP"`
	LinkedNginxID    string `json:"linkedNginxId,omitempty"`
	LinkedRun        string `json:"linkedRun,omitempty"`
	ServiceName      string `json:"serviceName,omitempty"`
	ServiceAutoStart bool   `json:"serviceAutoStart"`
	DirectoryName    string `json:"directoryName,omitempty"`
	ID               string `json:"id"`
	PackageID        string `json:"packageId"`
	Name             string `json:"name"`
	Component        string `json:"component"`
	Version          string `json:"version"`
	Port             int    `json:"port"`
	Path             string `json:"path"`
	Status           string `json:"status"`
	PID              int    `json:"pid"`
	Error            string `json:"error"`
}
type Site struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	NginxID  string `json:"nginxId"`
	PHPID    string `json:"phpId"`
	URL      string `json:"url"`
	Port     int    `json:"port"`
	Template string `json:"template"`
}
type Task struct {
	ComposerArtifact  *ComposerArtifact     `json:"composerArtifact,omitempty"`
	Kind              string                `json:"kind,omitempty"`
	ExtensionName     string                `json:"extensionName,omitempty"`
	InstanceID        string                `json:"instanceId,omitempty"`
	EnableExtension   bool                  `json:"enableExtension,omitempty"`
	ConfigRevision    string                `json:"configRevision,omitempty"`
	ExtensionArtifact *PHPExtensionArtifact `json:"extensionArtifact,omitempty"`
	Warning           string                `json:"warning,omitempty"`
	ID                string                `json:"id"`
	ArtifactID        string                `json:"artifactId"`
	Component         string                `json:"component"`
	Version           string                `json:"version"`
	Status            string                `json:"status"`
	Source            string                `json:"source"`
	SourceURL         string                `json:"sourceUrl"`
	ETag              string                `json:"etag"`
	Received          int64                 `json:"received"`
	Total             int64                 `json:"total"`
	Speed             int64                 `json:"speed"`
	Error             string                `json:"error"`
	CreatedAt         string                `json:"createdAt"`
}
type Settings struct {
	System             SystemSettings `json:"system"`
	ThemeMode          string         `json:"themeMode"`
	ProxyMode          string         `json:"proxyMode"`
	ProxyURL           string         `json:"proxyUrl"`
	SourceMode         string         `json:"sourceMode"`
	CatalogAutoUpdate  bool           `json:"catalogAutoUpdate"`
	CatalogUpdateHours int            `json:"catalogUpdateHours"`
	CatalogURLs        []string       `json:"catalogUrls"`
}
type SystemSettings struct {
	AutoStart      bool   `json:"autoStart"`
	StartMinimized bool   `json:"startMinimized"`
	CloseBehavior  string `json:"closeBehavior"`
}
type CatalogStatus struct {
	Refreshing  bool     `json:"refreshing"`
	LastAttempt string   `json:"lastAttempt"`
	LastSuccess string   `json:"lastSuccess"`
	Added       int      `json:"added"`
	Warnings    []string `json:"warnings"`
}
type State struct {
	RuntimeUserSID          string                  `json:"runtimeUserSid,omitempty"`
	Tools                   ToolchainSettings       `json:"tools"`
	ExtensionArtifacts      []PHPExtensionArtifact  `json:"extensionArtifacts"`
	ExtensionPackages       []InstalledPHPExtension `json:"extensionPackages"`
	ExtensionCatalogUpdated map[string]string       `json:"extensionCatalogUpdated"`
	PreparedExtensions      []PreparedPHPExtension  `json:"preparedExtensions"`
	Revision                uint64                  `json:"revision"`
	Root                    string                  `json:"root"`
	Artifacts               []Artifact              `json:"artifacts"`
	Packages                []InstalledPackage      `json:"packages"`
	Instances               []Instance              `json:"instances"`
	Sites                   []Site                  `json:"sites"`
	Tasks                   []Task                  `json:"tasks"`
	Settings                Settings                `json:"settings"`
	Catalog                 CatalogStatus           `json:"catalog"`
}
type StateUpdate struct {
	Revision uint64 `json:"revision"`
	Changed  bool   `json:"changed"`
	State    *State `json:"state,omitempty"`
}
