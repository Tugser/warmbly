package handler

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/app/fleetnode"
	"github.com/warmbly/warmbly/internal/app/keystatus"
	"github.com/warmbly/warmbly/internal/errx"
)

// keyStatusTTL bounds how often the panel can make the backend scan every sealed column.
const keyStatusTTL = time.Minute

type keyStatusCache struct {
	mu     sync.Mutex
	at     time.Time
	report *keystatus.Report
}

var adminKeyStatus keyStatusCache

type adminKeyStatusResponse struct {
	Keys  *keystatus.Report     `json:"keys"`
	Fleet *fleetnode.EnvStatus  `json:"fleet,omitempty"`
	Ready adminKeyStatusReadies `json:"ready"`
}

// adminKeyStatusReadies says which retirements the evidence already allows.
type adminKeyStatusReadies struct {
	RetireReadKeys bool `json:"retire_read_keys"`
	RetireTokens   bool `json:"retire_tokens"`
}

// AdminKeyStatus reports where every sealed value stands against the configured
// keys and whether every node runs the current env. Read only: it never reseals.
func (h *Handler) AdminKeyStatus(c *gin.Context) {
	if h.KeyStatusDB == nil {
		errx.Handle(c, errx.New(errx.NotImplemented, "Key status is not available on this instance."))
		return
	}
	report, err := h.keyStatus(c.Request.Context(), c.Query("refresh") == "true")
	if err != nil {
		errx.Handle(c, err)
		return
	}
	resp := adminKeyStatusResponse{Keys: report}
	resp.Ready.RetireReadKeys = report.InstanceReadKeys+report.MFAReadKeys > 0 && report.OnReadKey() == 0 && report.Unreadable() == 0
	if h.FleetNodes != nil {
		nodes, err := h.FleetNodes.List(c.Request.Context(), "")
		if err != nil {
			errx.Handle(c, err)
			return
		}
		fleet := fleetnode.EnvStatusOf(nodes)
		resp.Fleet = &fleet
		resp.Ready.RetireTokens = fleet.AllCurrent
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) keyStatus(ctx context.Context, refresh bool) (*keystatus.Report, error) {
	adminKeyStatus.mu.Lock()
	defer adminKeyStatus.mu.Unlock()
	if !refresh && adminKeyStatus.report != nil && time.Since(adminKeyStatus.at) < keyStatusTTL {
		return adminKeyStatus.report, nil
	}
	keys, err := keystatus.LoadKeyring(ctx)
	if err != nil {
		log.Printf("admin: key status keyring: %v", err)
		return nil, errx.NewPublic(errx.Unprocessable, "The instance keys could not be loaded. Run `warmblyctl keys status` inside the backend container for the reason.")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	report, err := keystatus.Scan(ctx, h.KeyStatusDB, keys, keystatus.Options{})
	if err != nil {
		log.Printf("admin: key status scan: %v", err)
		return nil, errx.InternalError()
	}
	adminKeyStatus.report, adminKeyStatus.at = &report, time.Now()
	return &report, nil
}
