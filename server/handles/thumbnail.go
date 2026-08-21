package handles

import (
	"errors"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/thumbnail"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

func CreateThumbnailTask(c *gin.Context) {
	createThumbnailTask(c, false)
}

func ScanThumbnailTask(c *gin.Context) {
	createThumbnailTask(c, true)
}

func createThumbnailTask(c *gin.Context, scanOnly bool) {
	var request thumbnail.CreateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	creator, _ := c.Request.Context().Value(conf.UserKey).(*model.User)
	task, err := thumbnail.Create(c.Request.Context(), request, creator, scanOnly)
	if err != nil {
		code := 422
		var kindErr *thumbnail.KindError
		if errors.As(err, &kindErr) {
			switch kindErr.Kind {
			case thumbnail.ErrorInvalid:
				code = 400
			case thumbnail.ErrorConflict:
				code = 409
			}
		}
		common.ErrorResp(c, err, code)
		return
	}
	common.SuccessResp(c, gin.H{"task_id": task.GetID()})
}

func GetThumbnailTaskDetail(c *gin.Context) {
	task, ok := thumbnail.TaskManager.GetByID(c.Query("tid"))
	if !ok {
		common.ErrorStrResp(c, "task not found", 404)
		return
	}
	common.SuccessResp(c, task.Detail())
}

func GetThumbnailCapabilities(c *gin.Context) {
	common.SuccessResp(c, thumbnail.GetCapabilities(c.Request.Context(), true))
}
