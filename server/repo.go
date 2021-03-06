package server

import (
	"encoding/base32"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/securecookie"

	"github.com/drone/drone/cache"
	"github.com/drone/drone/remote"
	"github.com/drone/drone/router/middleware/session"
	"github.com/drone/drone/shared/httputil"
	"github.com/drone/drone/shared/token"
	"github.com/drone/drone/store"
)

func PostRepo(c *gin.Context) {
	remote := remote.FromContext(c)
	user := session.User(c)
	owner := c.Param("owner")
	name := c.Param("name")

	if user == nil {
		c.AbortWithStatus(403)
		return
	}

	r, err := remote.Repo(user, owner, name)
	if err != nil {
		c.String(404, err.Error())
		return
	}
	m, err := cache.GetPerms(c, user, owner, name)
	if err != nil {
		c.String(404, err.Error())
		return
	}
	if !m.Admin {
		c.String(403, "Administrative access is required.")
		return
	}

	// error if the repository already exists
	_, err = store.GetRepoOwnerName(c, owner, name)
	if err == nil {
		c.String(409, "Repository already exists.")
		return
	}

	// set the repository owner to the
	// currently authenticated user.
	r.UserID = user.ID
	r.AllowPush = true
	r.AllowPull = true
	r.Timeout = 60 // 1 hour default build time
	r.Hash = base32.StdEncoding.EncodeToString(
		securecookie.GenerateRandomKey(32),
	)

	// crates the jwt token used to verify the repository
	t := token.New(token.HookToken, r.FullName)
	sig, err := t.Sign(r.Hash)
	if err != nil {
		c.String(500, err.Error())
		return
	}

	link := fmt.Sprintf(
		"%s/hook?access_token=%s",
		httputil.GetURL(c.Request),
		sig,
	)

	// activate the repository before we make any
	// local changes to the database.
	err = remote.Activate(user, r, link)
	if err != nil {
		c.String(500, err.Error())
		return
	}

	// persist the repository
	err = store.CreateRepo(c, r)
	if err != nil {
		c.String(500, err.Error())
		return
	}

	c.JSON(200, r)
}

func PatchRepo(c *gin.Context) {
	repo := session.Repo(c)
	user := session.User(c)

	in := &struct {
		IsTrusted   *bool  `json:"trusted,omitempty"`
		Timeout     *int64 `json:"timeout,omitempty"`
		AllowPull   *bool  `json:"allow_pr,omitempty"`
		AllowPush   *bool  `json:"allow_push,omitempty"`
		AllowDeploy *bool  `json:"allow_deploy,omitempty"`
		AllowTag    *bool  `json:"allow_tag,omitempty"`
	}{}
	if err := c.Bind(in); err != nil {
		c.AbortWithError(http.StatusBadRequest, err)
		return
	}

	if in.AllowPush != nil {
		repo.AllowPush = *in.AllowPush
	}
	if in.AllowPull != nil {
		repo.AllowPull = *in.AllowPull
	}
	if in.AllowDeploy != nil {
		repo.AllowDeploy = *in.AllowDeploy
	}
	if in.AllowTag != nil {
		repo.AllowTag = *in.AllowTag
	}
	if in.IsTrusted != nil && user.Admin {
		repo.IsTrusted = *in.IsTrusted
	}
	if in.Timeout != nil && user.Admin {
		repo.Timeout = *in.Timeout
	}

	err := store.UpdateRepo(c, repo)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, repo)
}

func GetRepo(c *gin.Context) {
	c.JSON(http.StatusOK, session.Repo(c))
}

func DeleteRepo(c *gin.Context) {
	remote := remote.FromContext(c)
	repo := session.Repo(c)
	user := session.User(c)

	err := store.DeleteRepo(c, repo)
	if err != nil {
		c.AbortWithError(http.StatusInternalServerError, err)
		return
	}

	remote.Deactivate(user, repo, httputil.GetURL(c.Request))
	c.Writer.WriteHeader(http.StatusOK)
}
