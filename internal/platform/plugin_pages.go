package platform

import "encoding/json"

// PluginPage is the installed navigation projection. Opening still enforces
// the selected Project's plugin enablement, grants, settings and model binding.
type PluginPage struct {
	ID        string        `json:"id"`
	PluginID  string        `json:"pluginId"`
	ReleaseID string        `json:"releaseId"`
	Title     LocalizedText `json:"title"`
	Agent     bool          `json:"agent"`
}

func (m *Manager) pluginPages(release Release) ([]PluginPage, error) {
	labels := map[string]map[string]json.RawMessage{}
	for locale, path := range release.Manifest.Locales {
		data, err := m.readReleaseFile(release, path)
		if err != nil {
			return nil, err
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, err
		}
		labels[locale] = values
	}
	pages := []PluginPage{}
	for _, page := range release.Manifest.contributions().Pages {
		var title LocalizedText
		_ = json.Unmarshal(labels["zh-CN"][page.TitleKey], &title.Chinese)
		_ = json.Unmarshal(labels["en-US"][page.TitleKey], &title.English)
		pages = append(pages, PluginPage{ID: page.ID, PluginID: release.Manifest.ID, ReleaseID: release.Ref.ReleaseID, Title: title, Agent: page.Agent != nil})
	}
	return pages, nil
}

func (m *Manager) PluginPages() ([]PluginPage, error) {
	installed, err := m.List(Plugin)
	if err != nil {
		return nil, err
	}
	pages := []PluginPage{}
	for _, item := range installed {
		if item.Removed || !item.Enabled || item.Problem != nil {
			continue
		}
		release, _, err := m.release(ReleaseRef{Package: PackageRef{Kind: Plugin, ID: item.ID}, ReleaseID: item.CurrentRelease})
		if err != nil {
			return nil, err
		}
		entries, err := m.pluginPages(release)
		if err != nil {
			return nil, err
		}
		pages = append(pages, entries...)
	}
	return pages, nil
}
