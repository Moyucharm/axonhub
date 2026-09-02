package biz

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"go.uber.org/fx"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/cpainstance"
	"github.com/looplj/axonhub/internal/ent/cpausageevent"
	"github.com/looplj/axonhub/internal/log"
	cpaclient "github.com/looplj/axonhub/internal/server/biz/cpa"
)

const (
	defaultCPARefreshIntervalMinutes = 5
	minCPARefreshIntervalMinutes     = 5
	maxCPARefreshIntervalMinutes     = 1440
	defaultCPAEnabledPatrolMinutes   = 5
	minCPAEnabledPatrolMinutes       = 1
	maxCPAEnabledPatrolMinutes       = 1440
	defaultCPADisabledPatrolMinutes  = 480
	minCPADisabledPatrolMinutes      = 60
	maxCPADisabledPatrolMinutes      = 10080
	maxCPAInstanceConcurrency        = 4
	maxCPAGlobalConcurrency          = 8
	cpaRefreshDispatcherInterval     = 5 * time.Second
)

// CPAServiceParams contains CPA service dependencies.
type CPAServiceParams struct {
	fx.In

	Ent            *ent.Client
	SystemService  *SystemService
	ChannelService *ChannelService
}

// CPAService manages CLIProxyAPI connections, credentials, and quota snapshots.
type CPAService struct {
	*AbstractService

	SystemService     *SystemService
	ChannelService    *ChannelService
	quotaRegistry     *cpaclient.QuotaRegistry
	connections       *cpaConnectionProvider
	quotaExecutor     *cpaQuotaExecutor
	repository        *cpaRepository
	syncGroup         singleflight.Group
	instanceWriteMu   sync.Mutex
	instanceWrite     map[int]*cpaInstanceWriteEntry
	usageWriteMu      sync.Mutex
	usageWrite        *semaphore.Weighted
	refreshProgressMu sync.Mutex
	refreshProgress   map[int]*cpaRefreshProgressState
	now               func() time.Time
	jitter            func() time.Duration

	usageRepository           *cpaUsageRepository
	pricingRepository         *cpaPricingRepository
	usageCollectorReconcileMu sync.Mutex
	usageCollectorMu          sync.Mutex
	usageCollectors           map[int]*usageCollectorWorker
	usageCollectorStarted     bool
}

// NewCPAService creates the CPA management service.
func NewCPAService(params CPAServiceParams) *CPAService {
	svc := &CPAService{
		AbstractService: &AbstractService{db: params.Ent},
		SystemService:   params.SystemService,
		ChannelService:  params.ChannelService,
		quotaRegistry:   cpaclient.NewQuotaRegistry(),
		instanceWrite:   make(map[int]*cpaInstanceWriteEntry),
		usageWrite:      semaphore.NewWeighted(1),
		refreshProgress: make(map[int]*cpaRefreshProgressState),
		now:             func() time.Time { return time.Now().UTC() },
		jitter: func() time.Duration {
			return time.Duration(15+rand.IntN(46)) * time.Second
		},
	}
	svc.connections = newCPAConnectionProvider(params.SystemService)
	svc.usageRepository = newCPAUsageRepository(
		params.Ent,
		svc.withCPAInstanceWriteLock,
		svc.withCPAInstanceWriteRetry,
		svc.withCPAUsageWriteRetry,
		svc.now,
	)
	svc.pricingRepository = newCPAPricingRepository(
		params.Ent,
		func() []*Channel {
			if svc.ChannelService == nil {
				return nil
			}
			return svc.ChannelService.GetEnabledChannels()
		},
		svc.now,
	)
	svc.repository = newCPARepository(svc)
	svc.quotaExecutor = newCPAQuotaExecutor(svc.quotaRegistry, svc.applyQuotaEstimate, svc.now)
	return svc
}

// CreateCPAInstanceInput creates one CPA connection.
type CreateCPAInstanceInput struct {
	Name                          string
	BaseURL                       string
	ManagementSecret              string
	Enabled                       *bool
	InsecureSkipTLS               bool
	AutoRefreshEnabled            *bool
	RefreshIntervalMinutes        *int
	AutoManageEnabled             *bool
	UsageStreamEnabled            *bool
	EnabledPatrolIntervalMinutes  *int
	DisabledPatrolIntervalMinutes *int
}

// UpdateCPAInstanceInput updates one CPA connection. An empty secret preserves the stored secret.
type UpdateCPAInstanceInput struct {
	Name                          *string
	BaseURL                       *string
	ManagementSecret              *string
	Enabled                       *bool
	InsecureSkipTLS               *bool
	AutoRefreshEnabled            *bool
	RefreshIntervalMinutes        *int
	AutoManageEnabled             *bool
	UsageStreamEnabled            *bool
	EnabledPatrolIntervalMinutes  *int
	DisabledPatrolIntervalMinutes *int
}

// CPAConnectionStatus describes the local management connection state.
type CPAConnectionStatus string

const (
	CPAConnectionStatusConnected CPAConnectionStatus = "connected"
	CPAConnectionStatusDisabled  CPAConnectionStatus = "disabled"
	CPAConnectionStatusError     CPAConnectionStatus = "error"
	CPAConnectionStatusUnknown   CPAConnectionStatus = "unknown"
)

// CPAInstanceView is the safe API representation of a CPA instance.
type CPAInstanceView struct {
	ID                            int
	Name                          string
	BaseURL                       string
	Enabled                       bool
	InsecureSkipTLS               bool
	AutoRefreshEnabled            bool
	RefreshIntervalMinutes        int
	AutoManageEnabled             bool
	UsageStreamEnabled            bool
	EnabledPatrolIntervalMinutes  int
	DisabledPatrolIntervalMinutes int
	NextRefreshAt                 *time.Time
	NextEnabledPatrolAt           *time.Time
	NextDisabledPatrolAt          *time.Time
	ServerVersion                 string
	ServerCommit                  string
	ServerBuildDate               string
	LastSyncAttemptAt             *time.Time
	LastSyncSuccessAt             *time.Time
	LastErrorAt                   *time.Time
	LastError                     *string
	HasSecret                     bool
	ConnectionStatus              CPAConnectionStatus
	CreatedAt                     time.Time
	UpdatedAt                     time.Time
}

func (svc *CPAService) CreateInstance(ctx context.Context, input CreateCPAInstanceInput) (*CPAInstanceView, error) {
	config, err := normalizeCreateCPAInstanceConfig(input)
	if err != nil {
		return nil, err
	}
	client, err := svc.newCPAClient(cpaclient.Config{
		BaseURL:          config.baseURL,
		ManagementSecret: config.managementSecret,
		InsecureSkipTLS:  config.insecureSkipTLS,
	})
	if err != nil {
		return nil, err
	}
	defer client.CloseIdleConnections()
	authFiles, buildInfo, err := client.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("validate CPA connection: %w", err)
	}

	encryptedSecret, err := svc.encryptSecret(ctx, config.managementSecret)
	if err != nil {
		return nil, err
	}
	now := svc.now()
	var created *ent.CPAInstance
	err = svc.RunInTransaction(ctx, func(txCtx context.Context) error {
		builder := svc.entFromContext(txCtx).CPAInstance.Create().
			SetName(config.name).
			SetBaseURL(config.baseURL).
			SetEncryptedSecret(encryptedSecret).
			SetEnabled(config.enabled).
			SetInsecureSkipTLS(config.insecureSkipTLS).
			SetAutoRefreshEnabled(config.autoRefreshEnabled).
			SetRefreshIntervalMinutes(config.refreshIntervalMinutes).
			SetAutoManageEnabled(config.autoManageEnabled).
			SetUsageStreamEnabled(config.usageStreamEnabled).
			SetEnabledPatrolIntervalMinutes(config.enabledPatrolInterval).
			SetDisabledPatrolIntervalMinutes(config.disabledPatrolInterval).
			SetServerVersion(buildInfo.Version).
			SetServerCommit(buildInfo.Commit).
			SetServerBuildDate(buildInfo.BuildDate).
			SetLastSyncAttemptAt(now).
			SetLastSyncSuccessAt(now)
		if config.enabled && config.autoRefreshEnabled {
			builder.SetNextRefreshAt(now.Add(svc.jitter()))
		}
		if config.enabled && config.autoManageEnabled {
			builder.
				SetNextEnabledPatrolAt(now.Add(svc.jitter())).
				SetNextDisabledPatrolAt(now.Add(svc.jitter()))
		}
		created, err = builder.Save(txCtx)
		if err != nil {
			return fmt.Errorf("create CPA instance: %w", err)
		}
		return svc.syncCredentialSnapshot(txCtx, created, authFiles.Files, now)
	})
	if err != nil {
		return nil, err
	}
	svc.refreshUsageStreamsAsync()
	return buildCPAInstanceView(created), nil
}

func (svc *CPAService) UpdateInstance(ctx context.Context, id int, input UpdateCPAInstanceInput) (*CPAInstanceView, error) {
	current, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance: %w", err)
	}

	config, secretChanged, connectionChanged, err := mergeUpdateCPAInstanceConfig(current, input)
	if err != nil {
		return nil, err
	}
	secret := config.managementSecret
	var authFiles *cpaclient.AuthFilesResponse
	var buildInfo cpaclient.BuildInfo
	if connectionChanged {
		if !secretChanged {
			secret, err = svc.decryptSecret(ctx, current.EncryptedSecret)
			if err != nil {
				return nil, err
			}
		}
		client, clientErr := svc.newCPAClient(cpaclient.Config{
			BaseURL:          config.baseURL,
			ManagementSecret: secret,
			InsecureSkipTLS:  config.insecureSkipTLS,
		})
		if clientErr != nil {
			return nil, clientErr
		}
		authFiles, buildInfo, err = client.ListCredentials(ctx)
		client.CloseIdleConnections()
		if err != nil {
			return nil, fmt.Errorf("validate CPA connection: %w", err)
		}
	}
	encryptedSecret := current.EncryptedSecret
	if secretChanged {
		encryptedSecret, err = svc.encryptSecret(ctx, secret)
		if err != nil {
			return nil, err
		}
	}

	now := svc.now()
	var updated *ent.CPAInstance
	err = svc.withCPAInstanceWriteRetry(ctx, id, func() error {
		return svc.RunInTransaction(ctx, func(txCtx context.Context) error {
			builder := svc.entFromContext(txCtx).CPAInstance.UpdateOneID(id).
				SetName(config.name).
				SetBaseURL(config.baseURL).
				SetEncryptedSecret(encryptedSecret).
				SetEnabled(config.enabled).
				SetInsecureSkipTLS(config.insecureSkipTLS).
				SetAutoRefreshEnabled(config.autoRefreshEnabled).
				SetRefreshIntervalMinutes(config.refreshIntervalMinutes).
				SetAutoManageEnabled(config.autoManageEnabled).
				SetUsageStreamEnabled(config.usageStreamEnabled).
				SetEnabledPatrolIntervalMinutes(config.enabledPatrolInterval).
				SetDisabledPatrolIntervalMinutes(config.disabledPatrolInterval)
			if !config.enabled || !config.autoRefreshEnabled {
				builder.ClearNextRefreshAt()
			} else if connectionChanged || input.RefreshIntervalMinutes != nil || input.AutoRefreshEnabled != nil {
				builder.SetNextRefreshAt(now.Add(svc.jitter()))
			}
			patrolChanged := input.AutoManageEnabled != nil || input.EnabledPatrolIntervalMinutes != nil || input.DisabledPatrolIntervalMinutes != nil
			if !config.enabled || !config.autoManageEnabled {
				builder.ClearNextEnabledPatrolAt().ClearNextDisabledPatrolAt()
			} else if connectionChanged || patrolChanged {
				builder.
					SetNextEnabledPatrolAt(now.Add(svc.jitter())).
					SetNextDisabledPatrolAt(now.Add(svc.jitter()))
			}
			if connectionChanged {
				builder.
					SetServerVersion(buildInfo.Version).
					SetServerCommit(buildInfo.Commit).
					SetServerBuildDate(buildInfo.BuildDate).
					SetLastSyncAttemptAt(now).
					SetLastSyncSuccessAt(now).
					ClearLastError().
					ClearLastErrorAt()
			}
			updated, err = builder.Save(txCtx)
			if err != nil {
				return fmt.Errorf("update CPA instance: %w", err)
			}
			if authFiles != nil {
				return svc.syncCredentialSnapshot(txCtx, updated, authFiles.Files, now)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	svc.refreshUsageStreamsAsync()
	return buildCPAInstanceView(updated), nil
}

// DeleteInstance removes an instance and all of its locally synchronized
// credentials and usage events. The rows are deleted explicitly in the same
// transaction instead of relying on the database-level cascade: production
// migrations run with foreign keys disabled, so the schema's OnDelete(Cascade)
// annotation would otherwise leave orphaned credential snapshots and usage
// events behind.
func (svc *CPAService) DeleteInstance(ctx context.Context, id int) error {
	svc.usageCollectorReconcileMu.Lock()
	if err := svc.stopUsageCollectorForInstance(ctx, id); err != nil {
		svc.usageCollectorReconcileMu.Unlock()
		svc.refreshUsageStreamsAsync()
		return fmt.Errorf("stop CPA usage collector before delete: %w", err)
	}
	err := svc.withCPAInstanceWriteRetry(ctx, id, func() error {
		return svc.RunInTransaction(ctx, func(txCtx context.Context) error {
			if _, err := svc.entFromContext(txCtx).CpaUsageEvent.Delete().
				Where(cpausageevent.CpaInstanceIDEQ(id)).
				Exec(txCtx); err != nil {
				return fmt.Errorf("delete CPA instance usage events: %w", err)
			}
			if _, err := svc.entFromContext(txCtx).CPACredential.Delete().
				Where(cpacredential.CpaInstanceIDEQ(id)).
				Exec(txCtx); err != nil {
				return fmt.Errorf("delete CPA instance credentials: %w", err)
			}
			if err := svc.entFromContext(txCtx).CPAInstance.DeleteOneID(id).Exec(txCtx); err != nil {
				return fmt.Errorf("delete CPA instance: %w", err)
			}
			return nil
		})
	})
	svc.usageCollectorReconcileMu.Unlock()
	if err != nil {
		svc.refreshUsageStreamsAsync()
		return err
	}
	if svc.quotaExecutor != nil {
		svc.quotaExecutor.forgetInstance(id)
	}
	svc.refreshProgressMu.Lock()
	delete(svc.refreshProgress, id)
	svc.refreshProgressMu.Unlock()
	if svc.usageRepository != nil {
		svc.usageRepository.invalidateCredentialCache(id)
	}
	return nil
}

// refreshUsageStreamsAsync reconciles usage queue collectors without blocking the caller.
func (svc *CPAService) refreshUsageStreamsAsync() {
	svc.usageCollectorMu.Lock()
	started := svc.usageCollectorStarted
	svc.usageCollectorMu.Unlock()
	if !started {
		return
	}
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error(context.Background(), "refresh CPA usage collectors panicked", log.Any("panic", recovered))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := svc.RefreshUsageStreams(ctx); err != nil {
			log.Warn(ctx, "refresh CPA usage collectors failed", log.Cause(err))
		}
	}()
}

func (svc *CPAService) ListInstances(ctx context.Context) ([]*CPAInstanceView, error) {
	instances, err := svc.entFromContext(ctx).CPAInstance.Query().
		Order(cpainstance.ByName()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list CPA instances: %w", err)
	}
	result := make([]*CPAInstanceView, 0, len(instances))
	for _, instance := range instances {
		result = append(result, buildCPAInstanceView(instance))
	}
	return result, nil
}

func (svc *CPAService) GetInstance(ctx context.Context, id int) (*CPAInstanceView, error) {
	instance, err := svc.entFromContext(ctx).CPAInstance.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get CPA instance: %w", err)
	}
	return buildCPAInstanceView(instance), nil
}

func buildCPAInstanceView(instance *ent.CPAInstance) *CPAInstanceView {
	status := CPAConnectionStatusConnected
	if !instance.Enabled {
		status = CPAConnectionStatusDisabled
	} else if instance.LastError != nil && strings.TrimSpace(*instance.LastError) != "" {
		status = CPAConnectionStatusError
	} else if instance.LastSyncSuccessAt == nil {
		status = CPAConnectionStatusUnknown
	}
	return &CPAInstanceView{
		ID:                            instance.ID,
		Name:                          instance.Name,
		BaseURL:                       instance.BaseURL,
		Enabled:                       instance.Enabled,
		InsecureSkipTLS:               instance.InsecureSkipTLS,
		AutoRefreshEnabled:            instance.AutoRefreshEnabled,
		RefreshIntervalMinutes:        instance.RefreshIntervalMinutes,
		AutoManageEnabled:             instance.AutoManageEnabled,
		UsageStreamEnabled:            instance.UsageStreamEnabled,
		EnabledPatrolIntervalMinutes:  instance.EnabledPatrolIntervalMinutes,
		DisabledPatrolIntervalMinutes: instance.DisabledPatrolIntervalMinutes,
		NextRefreshAt:                 instance.NextRefreshAt,
		NextEnabledPatrolAt:           instance.NextEnabledPatrolAt,
		NextDisabledPatrolAt:          instance.NextDisabledPatrolAt,
		ServerVersion:                 instance.ServerVersion,
		ServerCommit:                  instance.ServerCommit,
		ServerBuildDate:               instance.ServerBuildDate,
		LastSyncAttemptAt:             instance.LastSyncAttemptAt,
		LastSyncSuccessAt:             instance.LastSyncSuccessAt,
		LastErrorAt:                   instance.LastErrorAt,
		LastError:                     instance.LastError,
		HasSecret:                     strings.TrimSpace(instance.EncryptedSecret) != "",
		ConnectionStatus:              status,
		CreatedAt:                     instance.CreatedAt,
		UpdatedAt:                     instance.UpdatedAt,
	}
}

func (svc *CPAService) supportsNormalizedQuota(credential cpaclient.NormalizedCredential) bool {
	return svc.quotaRegistry.Supports(credential.Provider) && !(credential.Provider == "xai" && credential.QuotaContext.Paid)
}

func (svc *CPAService) supportsStoredQuota(credential *ent.CPACredential) bool {
	return svc.quotaRegistry.Supports(credential.Provider) &&
		!(credential.Provider == "xai" && credential.QuotaContext.Paid)
}

func normalizeCPARefreshInterval(value *int) (int, error) {
	if value == nil {
		return defaultCPARefreshIntervalMinutes, nil
	}
	if *value < minCPARefreshIntervalMinutes || *value > maxCPARefreshIntervalMinutes {
		return 0, fmt.Errorf("CPA refresh interval must be between %d and %d minutes", minCPARefreshIntervalMinutes, maxCPARefreshIntervalMinutes)
	}
	return *value, nil
}

func normalizeCPAEnabledPatrolInterval(value *int) (int, error) {
	if value == nil {
		return defaultCPAEnabledPatrolMinutes, nil
	}
	if *value < minCPAEnabledPatrolMinutes || *value > maxCPAEnabledPatrolMinutes {
		return 0, fmt.Errorf("CPA enabled patrol interval must be between %d and %d minutes", minCPAEnabledPatrolMinutes, maxCPAEnabledPatrolMinutes)
	}
	return *value, nil
}

func normalizeCPADisabledPatrolInterval(value *int) (int, error) {
	if value == nil {
		return defaultCPADisabledPatrolMinutes, nil
	}
	if *value < minCPADisabledPatrolMinutes || *value > maxCPADisabledPatrolMinutes {
		return 0, fmt.Errorf("CPA disabled patrol interval must be between %d and %d minutes", minCPADisabledPatrolMinutes, maxCPADisabledPatrolMinutes)
	}
	return *value, nil
}

func (svc *CPAService) encryptSecret(ctx context.Context, secret string) (string, error) {
	return svc.connections.encryptSecret(ctx, secret)
}

func (svc *CPAService) decryptSecret(ctx context.Context, ciphertext string) (string, error) {
	return svc.connections.decryptSecret(ctx, ciphertext)
}

func (svc *CPAService) clientForInstance(ctx context.Context, instance *ent.CPAInstance) (cpaclient.ManagementClient, error) {
	return svc.connections.openInstance(ctx, instance)
}

func (svc *CPAService) newCPAClient(config cpaclient.Config) (cpaclient.ManagementClient, error) {
	return svc.connections.openConfig(config)
}
