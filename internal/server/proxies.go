package server

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"mailsender/internal/store"
)

// ---------- egress proxies (SOCKS5) ----------

type proxyView struct {
	store.Proxy
	HasPassword bool     `json:"has_password"`
	Accounts    []string `json:"accounts"` // names of the accounts using it
}

type proxyInput struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"` // empty on update = keep
	ClearPW  bool   `json:"clear_password"`
	Remark   string `json:"remark"`
	URL      string `json:"url"` // optional socks5://user:pass@host:port, fills the fields above
}

func (in *proxyInput) normalize() error {
	if u := strings.TrimSpace(in.URL); u != "" {
		if !strings.Contains(u, "://") {
			u = "socks5://" + u
		}
		pu, err := url.Parse(u)
		if err != nil || (pu.Scheme != "socks5" && pu.Scheme != "socks5h") || pu.Hostname() == "" {
			return errors.New("代理地址格式应为 socks5://用户名:密码@主机:端口")
		}
		in.Host = pu.Hostname()
		in.Port, _ = strconv.Atoi(pu.Port())
		if pu.User != nil {
			in.Username = pu.User.Username()
			in.Password, _ = pu.User.Password()
		}
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.Trim(strings.TrimSpace(in.Host), "[]")
	in.Username = strings.TrimSpace(in.Username)
	if in.Host == "" || strings.ContainsAny(in.Host, " /@:") && net.ParseIP(in.Host) == nil {
		return errors.New("代理主机无效")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return errors.New("代理端口无效")
	}
	if len(in.Username) > 255 || len(in.Password) > 255 {
		return errors.New("SOCKS5 用户名和密码最长 255 个字符")
	}
	if in.Name == "" {
		in.Name = net.JoinHostPort(in.Host, strconv.Itoa(in.Port))
	}
	return nil
}

func (s *Server) listProxies(c *gin.Context) {
	var ps []store.Proxy
	if err := s.db.DB.Order("id").Find(&ps).Error; err != nil {
		fail(c, err)
		return
	}
	var accs []store.Account
	s.db.DB.Select("id", "name", "proxy_id").Where("proxy_id > 0").Order("id").Find(&accs)
	used := map[uint][]string{}
	for _, a := range accs {
		used[a.ProxyID] = append(used[a.ProxyID], a.Name)
	}
	out := make([]proxyView, 0, len(ps))
	for _, p := range ps {
		names := used[p.ID]
		if names == nil {
			names = []string{}
		}
		out = append(out, proxyView{Proxy: p, HasPassword: p.PasswordEnc != "", Accounts: names})
	}
	c.JSON(200, out)
}

func (s *Server) saveProxy(c *gin.Context) {
	var p store.Proxy
	id := uint(0)
	if c.Param("id") != "" {
		var ok bool
		if id, ok = idParam(c); !ok {
			return
		}
		if err := s.db.DB.First(&p, id).Error; err != nil {
			notFoundOr(c, err)
			return
		}
	}
	var in proxyInput
	if c.ShouldBindJSON(&in) != nil {
		bad(c, "invalid body")
		return
	}
	if err := in.normalize(); err != nil {
		bad(c, err.Error())
		return
	}
	p.Name, p.Host, p.Port, p.Username, p.Remark = in.Name, in.Host, in.Port, in.Username, in.Remark
	if in.Password != "" || in.ClearPW || id == 0 {
		enc, err := s.box.Encrypt(in.Password)
		if err != nil {
			fail(c, err)
			return
		}
		p.PasswordEnc = enc
	}
	var err error
	if id == 0 {
		err = s.db.DB.Create(&p).Error
	} else {
		err = s.db.DB.Model(&p).Select("name", "host", "port", "username", "password_enc", "remark").Updates(&p).Error
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, p)
}

func (s *Server) deleteProxy(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var n int64
	s.db.DB.Model(&store.Account{}).Where("proxy_id = ?", id).Count(&n)
	if n > 0 {
		// Unbinding silently would move those accounts onto the server's own IP.
		bad(c, fmt.Sprintf("还有 %d 个账号在使用这个代理，请先把它们改为其他出口", n))
		return
	}
	if err := s.db.DB.Delete(&store.Proxy{}, id).Error; err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) checkProxy(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var p store.Proxy
	if err := s.db.DB.First(&p, id).Error; err != nil {
		notFoundOr(c, err)
		return
	}
	c.JSON(200, s.svc.CheckProxy(&p))
}

func (s *Server) directIP(c *gin.Context) { c.JSON(200, s.svc.DirectIP()) }

// proxyExists validates an account's proxy binding.
func (s *Server) proxyExists(id uint) error {
	if id == 0 {
		return nil
	}
	var n int64
	s.db.DB.Model(&store.Proxy{}).Where("id = ?", id).Count(&n)
	if n == 0 {
		return errors.New("所选出口代理不存在")
	}
	return nil
}
