package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type SystemSettings struct {
	RegistrationsEnabled       bool              `json:"registrationsEnabled"`
	GenerationsEnabled         bool              `json:"generationsEnabled"`
	PublishingEnabled          bool              `json:"publishingEnabled"`
	MarketplaceCheckoutEnabled bool              `json:"marketplaceCheckoutEnabled"`
	TaskCreationEnabled        bool              `json:"taskCreationEnabled"`
	PublicNotice               string            `json:"publicNotice"`
	SiteConfiguration          SiteConfiguration `json:"siteConfiguration"`
	UpdatedBy                  *uuid.UUID        `json:"updatedBy,omitempty"`
	UpdatedByHandle            *string           `json:"updatedByHandle,omitempty"`
	UpdatedAt                  time.Time         `json:"updatedAt"`
}

type LocalizedSiteText struct {
	EnUS string `json:"enUS"`
	ZhCN string `json:"zhCN"`
}

type SitePolicyContent struct {
	Terms      LocalizedSiteText `json:"terms"`
	Privacy    LocalizedSiteText `json:"privacy"`
	Cookies    LocalizedSiteText `json:"cookies"`
	Acceptable LocalizedSiteText `json:"acceptable"`
	AI         LocalizedSiteText `json:"ai"`
	Licensing  LocalizedSiteText `json:"licensing"`
	Refunds    LocalizedSiteText `json:"refunds"`
	Copyright  LocalizedSiteText `json:"copyright"`
}

type SiteConfiguration struct {
	SiteName    string            `json:"siteName"`
	ServerURL   string            `json:"serverUrl"`
	SiteIconURL string            `json:"siteIconUrl"`
	FooterText  LocalizedSiteText `json:"footerText"`
	Policies    SitePolicyContent `json:"policies"`
}

type SystemSettingUpdate struct {
	RegistrationsEnabled       bool   `json:"registrationsEnabled"`
	GenerationsEnabled         bool   `json:"generationsEnabled"`
	PublishingEnabled          bool   `json:"publishingEnabled"`
	MarketplaceCheckoutEnabled bool   `json:"marketplaceCheckoutEnabled"`
	TaskCreationEnabled        bool   `json:"taskCreationEnabled"`
	PublicNotice               string `json:"publicNotice"`
}

func (s *Service) GetSystemSettings(ctx context.Context) (SystemSettings, error) {
	var settings SystemSettings
	var configurationJSON []byte
	err := s.pool.QueryRow(ctx, `
		SELECT settings.registrations_enabled,settings.generations_enabled,settings.publishing_enabled,
		       settings.marketplace_checkout_enabled,settings.task_creation_enabled,settings.public_notice,
		       settings.site_configuration,settings.updated_by,users.handle,settings.updated_at
		FROM system_settings settings
		LEFT JOIN users ON users.id=settings.updated_by
		WHERE settings.singleton=true`).Scan(
		&settings.RegistrationsEnabled, &settings.GenerationsEnabled, &settings.PublishingEnabled,
		&settings.MarketplaceCheckoutEnabled, &settings.TaskCreationEnabled, &settings.PublicNotice,
		&configurationJSON, &settings.UpdatedBy, &settings.UpdatedByHandle, &settings.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SystemSettings{}, ErrNotFound
	} else if err != nil {
		return SystemSettings{}, err
	}
	if err := json.Unmarshal(configurationJSON, &settings.SiteConfiguration); err != nil {
		return SystemSettings{}, fmt.Errorf("decode site configuration: %w", err)
	}
	return settings, nil
}

func (s *Service) UpdateSystemSettings(ctx context.Context, actorID uuid.UUID, input SystemSettingUpdate, requestID string) (SystemSettings, error) {
	input.PublicNotice = strings.TrimSpace(input.PublicNotice)
	if len(input.PublicNotice) > 240 {
		return SystemSettings{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SystemSettings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var settingsID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE system_settings
		SET registrations_enabled=$1,generations_enabled=$2,publishing_enabled=$3,
		    marketplace_checkout_enabled=$4,task_creation_enabled=$5,public_notice=$6,
		    updated_by=$7,updated_at=now()
		WHERE singleton=true
		RETURNING id`, input.RegistrationsEnabled, input.GenerationsEnabled, input.PublishingEnabled,
		input.MarketplaceCheckoutEnabled, input.TaskCreationEnabled, input.PublicNotice, actorID).Scan(&settingsID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SystemSettings{}, ErrNotFound
	} else if err != nil {
		return SystemSettings{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
		VALUES($1,'admin.system_settings_updated','system_settings',$2,'Administrator updated current system settings',$3,
		       jsonb_build_object('registrationsEnabled',$4::boolean,'generationsEnabled',$5::boolean,
		                          'publishingEnabled',$6::boolean,'marketplaceCheckoutEnabled',$7::boolean,
		                          'taskCreationEnabled',$8::boolean))`, actorID, settingsID, requestID,
		input.RegistrationsEnabled, input.GenerationsEnabled, input.PublishingEnabled,
		input.MarketplaceCheckoutEnabled, input.TaskCreationEnabled); err != nil {
		return SystemSettings{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SystemSettings{}, err
	}
	return s.GetSystemSettings(ctx)
}

func (s *Service) GetSiteConfiguration(ctx context.Context) (SiteConfiguration, error) {
	var configurationJSON []byte
	if err := s.pool.QueryRow(ctx, `SELECT site_configuration FROM system_settings WHERE singleton=true`).Scan(&configurationJSON); errors.Is(err, pgx.ErrNoRows) {
		return SiteConfiguration{}, ErrNotFound
	} else if err != nil {
		return SiteConfiguration{}, err
	}
	var configuration SiteConfiguration
	if err := json.Unmarshal(configurationJSON, &configuration); err != nil {
		return SiteConfiguration{}, fmt.Errorf("decode public site configuration: %w", err)
	}
	return configuration, nil
}

func (s *Service) UpdateSiteConfiguration(ctx context.Context, actorID uuid.UUID, configuration SiteConfiguration, requestID string) (SiteConfiguration, error) {
	normalizeSiteConfiguration(&configuration)
	if !validSiteConfiguration(configuration) {
		return SiteConfiguration{}, ErrInvalid
	}
	configurationJSON, err := json.Marshal(configuration)
	if err != nil {
		return SiteConfiguration{}, fmt.Errorf("encode site configuration: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SiteConfiguration{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var settingsID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE system_settings
		SET site_configuration=$1,updated_by=$2,updated_at=now()
		WHERE singleton=true
		RETURNING id`, configurationJSON, actorID).Scan(&settingsID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteConfiguration{}, ErrNotFound
	} else if err != nil {
		return SiteConfiguration{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_events(actor_id,action,resource_type,resource_id,reason,request_id,metadata)
		VALUES($1,'admin.site_configuration_updated','system_settings',$2,'Administrator updated public site configuration',$3,
		       jsonb_build_object('siteName',$4::text,'serverUrl',$5::text,'siteIconUrl',$6::text))`,
		actorID, settingsID, requestID, configuration.SiteName, configuration.ServerURL, configuration.SiteIconURL); err != nil {
		return SiteConfiguration{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SiteConfiguration{}, err
	}
	return configuration, nil
}

func normalizeSiteConfiguration(configuration *SiteConfiguration) {
	configuration.SiteName = strings.TrimSpace(configuration.SiteName)
	configuration.ServerURL = strings.TrimRight(strings.TrimSpace(configuration.ServerURL), "/")
	configuration.SiteIconURL = strings.TrimSpace(configuration.SiteIconURL)
	configuration.FooterText.EnUS = strings.TrimSpace(configuration.FooterText.EnUS)
	configuration.FooterText.ZhCN = strings.TrimSpace(configuration.FooterText.ZhCN)
	policies := []*LocalizedSiteText{
		&configuration.Policies.Terms, &configuration.Policies.Privacy, &configuration.Policies.Cookies, &configuration.Policies.Acceptable,
		&configuration.Policies.AI, &configuration.Policies.Licensing, &configuration.Policies.Refunds, &configuration.Policies.Copyright,
	}
	for _, policy := range policies {
		policy.EnUS = strings.TrimSpace(policy.EnUS)
		policy.ZhCN = strings.TrimSpace(policy.ZhCN)
	}
}

func validSiteConfiguration(configuration SiteConfiguration) bool {
	if len(configuration.SiteName) < 2 || len(configuration.SiteName) > 80 || !validAbsoluteHTTPURL(configuration.ServerURL) || !validSiteIconURL(configuration.SiteIconURL) ||
		len(configuration.FooterText.EnUS) > 1000 || len(configuration.FooterText.ZhCN) > 1000 {
		return false
	}
	policies := []LocalizedSiteText{
		configuration.Policies.Terms, configuration.Policies.Privacy, configuration.Policies.Cookies, configuration.Policies.Acceptable,
		configuration.Policies.AI, configuration.Policies.Licensing, configuration.Policies.Refunds, configuration.Policies.Copyright,
	}
	for _, policy := range policies {
		if len(policy.EnUS) > 50000 || len(policy.ZhCN) > 50000 {
			return false
		}
	}
	return true
}

func validAbsoluteHTTPURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && len(value) <= 2048
}

func validSiteIconURL(value string) bool {
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && len(value) <= 2048 {
		return true
	}
	return validAbsoluteHTTPURL(value)
}
