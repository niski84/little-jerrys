package jerry

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

func campaignWeight(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	weight, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("campaign weight must be an integer")
	}
	if err := validation.Validate(weight, validation.Required, validation.Min(1), validation.Max(10)); err != nil {
		return 0, err
	}
	return weight, nil
}

func registerCampaignHandlers(playback *http.ServeMux, app *App) {
	finish := func(w http.ResponseWriter, r *http.Request) {
		app.ApplyState()
		app.Playlist.Regenerate()
		app.SnapshotQueue()
		http.Redirect(w, r, "/commercials", http.StatusSeeOther)
	}
	playback.HandleFunc("POST /commercials/upload", func(w http.ResponseWriter, r *http.Request) {
		path, err := uploadFile(r, "commercial_file", filepath.Join(app.Cfg.MediaRoot, "commercials"), maxCommercialUpload, commercialUploadTypes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		name := strings.TrimSpace(r.FormValue("campaign"))
		if name != "" {
			if err := app.Store.Update(func(s *State) {
				for i := range s.CommercialCampaigns {
					if strings.EqualFold(s.CommercialCampaigns[i].Name, name) {
						s.CommercialCampaigns[i].Files = append(s.CommercialCampaigns[i].Files, path)
						return
					}
				}
				s.CommercialCampaigns = append(s.CommercialCampaigns, CommercialCampaign{ID: fmt.Sprintf("campaign-%d", time.Now().UnixNano()), Name: name, Enabled: true, Weight: 1, Files: []string{path}})
			}); err != nil {
				http.Error(w, "Upload saved but campaign state could not be saved: "+err.Error(), 500)
				return
			}
		}
		if err := app.Playlist.Scan(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		finish(w, r)
	})
	playback.HandleFunc("POST /commercials/campaigns", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		name := strings.TrimSpace(r.PostFormValue("name"))
		if err := validation.Validate(name, validation.Required, validation.Length(1, 120)); err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		weight, err := campaignWeight(r.PostFormValue("weight"), 1)
		if err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		if err := app.Store.Update(func(s *State) {
			for i := range s.CommercialCampaigns {
				if strings.EqualFold(s.CommercialCampaigns[i].Name, name) {
					s.CommercialCampaigns[i].Enabled = true
					s.CommercialCampaigns[i].Weight = weight
					return
				}
			}
			s.CommercialCampaigns = append(s.CommercialCampaigns, CommercialCampaign{ID: fmt.Sprintf("campaign-%d", time.Now().UnixNano()), Name: name, Enabled: true, Weight: weight})
		}); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		finish(w, r)
	})
	playback.HandleFunc("POST /commercials/campaigns/save", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		weights := map[string]int{}
		for _, c := range app.Store.Get().CommercialCampaigns {
			weight, err := campaignWeight(r.PostFormValue("cwmp:"+c.ID), c.Weight)
			if err != nil {
				http.Error(w, err.Error(), 422)
				return
			}
			weights[c.ID] = weight
		}
		if err := app.Store.Update(func(s *State) {
			for i := range s.CommercialCampaigns {
				c := &s.CommercialCampaigns[i]
				if weight, ok := weights[c.ID]; ok {
					c.Enabled = r.PostFormValue("ce:"+c.ID) == "1"
					c.Weight = weight
				}
			}
		}); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		finish(w, r)
	})
}
