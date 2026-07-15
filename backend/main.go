package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────────────────────────────────────────────
// GLOBALS
// ─────────────────────────────────────────────────────────────

var (
	db        *pgxpool.Pool
	jwtSecret []byte
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// ─────────────────────────────────────────────────────────────
// MODELS
// ─────────────────────────────────────────────────────────────

type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Active   bool   `json:"active"`
}

type CartItem struct {
	PID   string `json:"pid"`
	Name  string `json:"name"`
	Qty   int    `json:"qty"`
	Price int64  `json:"price"`
}

type BidaStateAPI struct {
	TableID   string     `json:"table_id"`
	Running   bool       `json:"running"`
	Paused    bool       `json:"paused"`
	StartTime *time.Time `json:"start_time"`
	PausedMs  int64      `json:"paused_ms"`
	Cart      []CartItem `json:"cart"`
}

type Tab struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	Items     []CartItem `json:"items"`
	BidaID    *string    `json:"bida_id"`
	BidaCost  int64      `json:"bida_cost"` // tiền giờ bida đã chốt khi gộp bida vào tab
}

type Session struct {
	ID      string          `json:"id"`
	Tab     string          `json:"tab"`
	Staff   string          `json:"staff"`
	Bida    int64           `json:"bida"`
	Cart    int64           `json:"cart"`
	Total   int64           `json:"total"`
	Time    string          `json:"time"`
	Date    string          `json:"date"`
	PaidAt  time.Time       `json:"paid_at"`
	Details json.RawMessage `json:"details,omitempty"`
}

type Product struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Unit     string `json:"unit"`
	Cat      string `json:"cat"`
	Stock    int    `json:"stock"`
	MinStock int    `json:"min_stock"`
	Active   bool   `json:"active"`
}

type InvImport struct {
	ID          int    `json:"id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"name"`
	Qty         int    `json:"qty"`
	UnitCost    int64  `json:"unit_cost"`
	TotalCost   int64  `json:"cost"`
	Note        string `json:"note"`
	Time        string `json:"time"`
	Date        string `json:"date"`
}

type Debt struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Phone     string          `json:"phone"`
	Orig      int64           `json:"orig"`
	Remaining int64           `json:"remaining"`
	Note      string          `json:"note"`
	Status    string          `json:"status"`
	Date      string          `json:"date"`
	Details   json.RawMessage `json:"details,omitempty"`
}

type Adjustment struct {
	ID      string `json:"id"`
	Desc    string `json:"desc"`
	Amt     int64  `json:"amt"`
	AdjType int    `json:"type"`
	Date    string `json:"date"`
}

type Expense struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Desc     string `json:"desc"`
	Amount   int64  `json:"amount"`
	Month    string `json:"month"`
	Category string `json:"category"`
}

type Fund struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Amount int64  `json:"amount"` // số dư hiện tại (cộng dồn từ fund_txns)
}

type FundTxn struct {
	ID     string `json:"id"`
	FundID string `json:"fund_id"`
	Amount int64  `json:"amount"` // + nạp, - rút
	Note   string `json:"note"`
	Date   string `json:"date"`
	Time   string `json:"time"`
}

// ─────────────────────────────────────────────────────────────
// JWT
// ─────────────────────────────────────────────────────────────

type Claims struct {
	UserID   int    `json:"user_id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

func generateToken(u User) (string, error) {
	claims := Claims{
		UserID:   u.ID,
		Username: u.Username,
		Name:     u.Name,
		Role:     u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecret)
}

func parseToken(tokenStr string) (*Claims, error) {
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if c, ok := t.Claims.(*Claims); ok && t.Valid {
		return c, nil
	}
	return nil, fmt.Errorf("invalid token")
}

type ctxKey string

const claimsKey ctxKey = "claims"

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			jsonErr(w, "Unauthorized", 401)
			return
		}
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			jsonErr(w, "Invalid token", 401)
			return
		}
		claims, err := parseToken(parts[1])
		if err != nil {
			jsonErr(w, "Invalid token", 401)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if getClaims(r).Role != "admin" {
			jsonErr(w, "Forbidden", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getClaims(r *http.Request) *Claims {
	return r.Context().Value(claimsKey).(*Claims)
}

// ─────────────────────────────────────────────────────────────
// HELPERS
// ─────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func decode(r *http.Request, dst interface{}) error {
	return json.NewDecoder(r.Body).Decode(dst)
}

func genID(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixMilli())
}

var vietLoc *time.Location

func initLoc() {
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		loc = time.UTC
	}
	vietLoc = loc
}

func vTime(t time.Time) string  { return t.In(vietLoc).Format("15:04") }
func vDate(t time.Time) string  { return t.In(vietLoc).Format("2006-01-02") }
func vMonth(t time.Time) string { return t.In(vietLoc).Format("01/2006") }

// ─────────────────────────────────────────────────────────────
// WEBSOCKET HUB
// ─────────────────────────────────────────────────────────────

type WSMsg struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type Hub struct {
	clients  map[*wsClient]bool
	bcast    chan []byte
	reg      chan *wsClient
	unreg    chan *wsClient
	mu       sync.Mutex
}

type wsClient struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte
}

var hub = &Hub{
	clients: make(map[*wsClient]bool),
	bcast:   make(chan []byte, 512),
	reg:     make(chan *wsClient),
	unreg:   make(chan *wsClient),
}

func (h *Hub) run() {
	for {
		select {
		case c := <-h.reg:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
		case c := <-h.unreg:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
		case msg := <-h.bcast:
			h.mu.Lock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					close(c.send)
					delete(h.clients, c)
				}
			}
			h.mu.Unlock()
		}
	}
}

func broadcast(msgType string, data interface{}) {
	b, _ := json.Marshal(WSMsg{Type: msgType, Data: data})
	select {
	case hub.bcast <- b:
	default:
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin:     func(r *http.Request) bool { return true },
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if _, err := parseToken(token); err != nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &wsClient{hub: hub, conn: conn, send: make(chan []byte, 256)}
	hub.reg <- c
	go c.write()
	go c.read()
}

func (c *wsClient) read() {
	defer func() { c.hub.unreg <- c; c.conn.Close() }()
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			break
		}
	}
}

func (c *wsClient) write() {
	ticker := time.NewTicker(25 * time.Second)
	defer func() { ticker.Stop(); c.conn.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, msg)
		case <-ticker.C:
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: AUTH
// ─────────────────────────────────────────────────────────────

func hLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	var u User
	var hash string
	err := db.QueryRow(r.Context(),
		`SELECT id,username,name,role,active,password_hash FROM users WHERE username=$1`, req.Username,
	).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active, &hash)
	if err != nil || !u.Active {
		jsonErr(w, "Tên đăng nhập hoặc mật khẩu không đúng", 401)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		jsonErr(w, "Tên đăng nhập hoặc mật khẩu không đúng", 401)
		return
	}
	token, err := generateToken(u)
	if err != nil {
		jsonErr(w, "Server error", 500)
		return
	}
	jsonOK(w, map[string]interface{}{"token": token, "user": u})
}

func hMe(w http.ResponseWriter, r *http.Request) {
	c := getClaims(r)
	jsonOK(w, map[string]interface{}{
		"id": c.UserID, "username": c.Username, "name": c.Name, "role": c.Role,
	})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: SETTINGS
// ─────────────────────────────────────────────────────────────

func hGetSettings(w http.ResponseWriter, r *http.Request) {
	var val []byte
	err := db.QueryRow(r.Context(), `SELECT value FROM settings WHERE key='global'`).Scan(&val)
	if err != nil {
		jsonErr(w, "Not found", 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(val)
}

func hPutSettings(w http.ResponseWriter, r *http.Request) {
	var val json.RawMessage
	if err := decode(r, &val); err != nil {
		jsonErr(w, "Invalid JSON", 400)
		return
	}
	_, err := db.Exec(r.Context(),
		`INSERT INTO settings(key,value) VALUES('global',$1) ON CONFLICT(key) DO UPDATE SET value=$1,updated_at=NOW()`, val)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("settings", val)
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: BIDA STATE
// ─────────────────────────────────────────────────────────────

func hGetBidaState(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT table_id,running,paused,start_time,paused_ms,cart FROM bida_state ORDER BY table_id`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	result := map[string]*BidaStateAPI{}
	for rows.Next() {
		var s BidaStateAPI
		var cartB []byte
		if err := rows.Scan(&s.TableID, &s.Running, &s.Paused, &s.StartTime, &s.PausedMs, &cartB); err != nil {
			continue
		}
		json.Unmarshal(cartB, &s.Cart)
		if s.Cart == nil {
			s.Cart = []CartItem{}
		}
		result[s.TableID] = &s
	}
	jsonOK(w, result)
}

func hPutBidaState(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var s BidaStateAPI
	if err := decode(r, &s); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	s.TableID = id
	if s.Cart == nil {
		s.Cart = []CartItem{}
	}
	cartB, _ := json.Marshal(s.Cart)
	_, err := db.Exec(r.Context(),
		`INSERT INTO bida_state(table_id,running,paused,start_time,paused_ms,cart,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,NOW())
		 ON CONFLICT(table_id) DO UPDATE SET running=$2,paused=$3,start_time=$4,paused_ms=$5,cart=$6,updated_at=NOW()`,
		s.TableID, s.Running, s.Paused, s.StartTime, s.PausedMs, cartB)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("bida", map[string]interface{}{id: s})
	jsonOK(w, s)
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: TABS
// ─────────────────────────────────────────────────────────────

func hGetTabs(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,name,created_at,items,bida_id,bida_cost FROM tabs ORDER BY created_at`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var tabs []Tab
	for rows.Next() {
		var t Tab
		var itemsB []byte
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedAt, &itemsB, &t.BidaID, &t.BidaCost); err != nil {
			continue
		}
		json.Unmarshal(itemsB, &t.Items)
		if t.Items == nil {
			t.Items = []CartItem{}
		}
		tabs = append(tabs, t)
	}
	if tabs == nil {
		tabs = []Tab{}
	}
	jsonOK(w, tabs)
}

func hCreateTab(w http.ResponseWriter, r *http.Request) {
	var t Tab
	if err := decode(r, &t); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if t.ID == "" {
		t.ID = genID("t")
	}
	if t.Items == nil {
		t.Items = []CartItem{}
	}
	t.CreatedAt = time.Now()
	itemsB, _ := json.Marshal(t.Items)
	_, err := db.Exec(r.Context(),
		`INSERT INTO tabs(id,name,created_at,items,bida_id,bida_cost) VALUES($1,$2,$3,$4,$5,$6)`,
		t.ID, t.Name, t.CreatedAt, itemsB, t.BidaID, t.BidaCost)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("tabs_add", t)
	jsonOK(w, t)
}

func hUpdateTab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var t Tab
	if err := decode(r, &t); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if t.Items == nil {
		t.Items = []CartItem{}
	}
	itemsB, _ := json.Marshal(t.Items)
	_, err := db.Exec(r.Context(),
		`UPDATE tabs SET name=$2,items=$3,bida_id=$4,bida_cost=$5 WHERE id=$1`,
		id, t.Name, itemsB, t.BidaID, t.BidaCost)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	t.ID = id
	broadcast("tab_update", t)
	jsonOK(w, t)
}

func hDeleteTab(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `DELETE FROM tabs WHERE id=$1`, id)
	broadcast("tabs_remove", id)
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: SESSIONS
// ─────────────────────────────────────────────────────────────

func hGetSessions(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, _ := strconv.Atoi(l); n > 0 {
			limit = n
		}
	}
	rows, err := db.Query(r.Context(),
		`SELECT id,tab_name,staff_name,bida_cost,cart_cost,total,paid_at,details FROM sessions ORDER BY paid_at DESC LIMIT $1`, limit)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Session
	for rows.Next() {
		var s Session
		var detB []byte
		if err := rows.Scan(&s.ID, &s.Tab, &s.Staff, &s.Bida, &s.Cart, &s.Total, &s.PaidAt, &detB); err != nil {
			continue
		}
		s.Time = vTime(s.PaidAt)
		s.Date = vDate(s.PaidAt)
		s.Details = detB
		list = append(list, s)
	}
	if list == nil {
		list = []Session{}
	}
	jsonOK(w, list)
}

func hCreateSession(w http.ResponseWriter, r *http.Request) {
	var s Session
	if err := decode(r, &s); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if s.ID == "" {
		s.ID = genID("s")
	}
	c := getClaims(r)
	if s.Staff == "" {
		s.Staff = c.Name
	}
	now := time.Now()
	s.PaidAt = now
	s.Time = vTime(now)
	s.Date = vDate(now)
	detB, _ := json.Marshal(s.Details)
	_, err := db.Exec(r.Context(),
		`INSERT INTO sessions(id,tab_name,staff_name,bida_cost,cart_cost,total,paid_at,details)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		s.ID, s.Tab, s.Staff, s.Bida, s.Cart, s.Total, now, detB)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("session_add", s)
	jsonOK(w, s)
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: INVENTORY
// ─────────────────────────────────────────────────────────────

func hGetInventory(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,name,price,unit,cat,stock,min_stock,active FROM inventory WHERE active=true ORDER BY cat,name`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Product
	for rows.Next() {
		var p Product
		rows.Scan(&p.ID, &p.Name, &p.Price, &p.Unit, &p.Cat, &p.Stock, &p.MinStock, &p.Active)
		list = append(list, p)
	}
	if list == nil {
		list = []Product{}
	}
	jsonOK(w, list)
}

func hCreateProduct(w http.ResponseWriter, r *http.Request) {
	var p Product
	if err := decode(r, &p); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if p.ID == "" {
		p.ID = genID("p")
	}
	p.Active = true
	_, err := db.Exec(r.Context(),
		`INSERT INTO inventory(id,name,price,unit,cat,stock,min_stock,active) VALUES($1,$2,$3,$4,$5,$6,$7,true)`,
		p.ID, p.Name, p.Price, p.Unit, p.Cat, p.Stock, p.MinStock)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("inventory_add", p)
	jsonOK(w, p)
}

func hUpdateProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var p Product
	if err := decode(r, &p); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	_, err := db.Exec(r.Context(),
		`UPDATE inventory SET name=$2,price=$3,unit=$4,cat=$5,stock=$6,min_stock=$7 WHERE id=$1`,
		id, p.Name, p.Price, p.Unit, p.Cat, p.Stock, p.MinStock)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	p.ID = id
	broadcast("inventory_update", p)
	jsonOK(w, p)
}

func hDeleteProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `UPDATE inventory SET active=false WHERE id=$1`, id)
	broadcast("inventory_remove", id)
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: IMPORTS
// ─────────────────────────────────────────────────────────────

func hGetImports(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, _ := strconv.Atoi(l); n > 0 {
			limit = n
		}
	}
	rows, err := db.Query(r.Context(),
		`SELECT id,COALESCE(product_id,''),COALESCE(product_name,''),qty,unit_cost,total_cost,COALESCE(note,''),COALESCE(imported_at,NOW()) FROM inv_imports ORDER BY imported_at DESC NULLS LAST LIMIT $1`, limit)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []InvImport
	for rows.Next() {
		var im InvImport
		var importedAt time.Time
		rows.Scan(&im.ID, &im.ProductID, &im.ProductName, &im.Qty, &im.UnitCost, &im.TotalCost, &im.Note, &importedAt)
		im.Time = vTime(importedAt)
		im.Date = vDate(importedAt)
		list = append(list, im)
	}
	if list == nil {
		list = []InvImport{}
	}
	jsonOK(w, list)
}

func hCreateImport(w http.ResponseWriter, r *http.Request) {
	var im InvImport
	if err := decode(r, &im); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	claims := getClaims(r)
	now := time.Now()
	im.Time = vTime(now)
	im.Date = vDate(now)
	err := db.QueryRow(r.Context(),
		`INSERT INTO inv_imports(product_id,product_name,qty,unit_cost,total_cost,note,staff_id,imported_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		im.ProductID, im.ProductName, im.Qty, im.UnitCost, im.TotalCost, im.Note, claims.UserID, now,
	).Scan(&im.ID)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	db.Exec(r.Context(), `UPDATE inventory SET stock=stock+$2 WHERE id=$1`, im.ProductID, im.Qty)
	jsonOK(w, im)
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: DEBTS
// ─────────────────────────────────────────────────────────────

func hGetDebts(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,name,COALESCE(phone,''),orig_amount,remaining,COALESCE(note,''),status,COALESCE(created_at,NOW()),COALESCE(details,'null') FROM debts ORDER BY created_at DESC NULLS LAST`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Debt
	for rows.Next() {
		var d Debt
		var createdAt time.Time
		var detB []byte
		rows.Scan(&d.ID, &d.Name, &d.Phone, &d.Orig, &d.Remaining, &d.Note, &d.Status, &createdAt, &detB)
		d.Date = vDate(createdAt)
		d.Details = detB
		list = append(list, d)
	}
	if list == nil {
		list = []Debt{}
	}
	jsonOK(w, list)
}

func hCreateDebt(w http.ResponseWriter, r *http.Request) {
	var d Debt
	if err := decode(r, &d); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if d.ID == "" {
		d.ID = genID("d")
	}
	d.Status = "pending"
	d.Remaining = d.Orig
	now := time.Now()
	d.Date = vDate(now)
	detB, _ := json.Marshal(d.Details)
	_, err := db.Exec(r.Context(),
		`INSERT INTO debts(id,name,phone,orig_amount,remaining,note,status,created_at,details)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		d.ID, d.Name, d.Phone, d.Orig, d.Remaining, d.Note, d.Status, now, detB)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("debt_update", d)
	jsonOK(w, d)
}

func hPayDebt(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Amount int64 `json:"amount"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	var d Debt
	err := db.QueryRow(r.Context(),
		`SELECT id,name,orig_amount,remaining FROM debts WHERE id=$1`, id,
	).Scan(&d.ID, &d.Name, &d.Orig, &d.Remaining)
	if err != nil {
		jsonErr(w, "Not found", 404)
		return
	}
	d.Remaining -= req.Amount
	if d.Remaining < 0 {
		d.Remaining = 0
	}
	if d.Remaining == 0 {
		d.Status = "paid"
	} else {
		d.Status = "partial"
	}
	db.Exec(r.Context(), `UPDATE debts SET remaining=$2,status=$3,updated_at=NOW() WHERE id=$1`, id, d.Remaining, d.Status)
	if req.Amount > 0 {
		aID := genID("a")
		db.Exec(r.Context(),
			`INSERT INTO adjustments(id,description,amount,adj_type,created_at) VALUES($1,$2,$3,1,NOW())`,
			aID, "Thu nợ — "+d.Name, req.Amount)
		broadcast("adjustment_add", Adjustment{ID: aID, Desc: "Thu nợ — " + d.Name, Amt: req.Amount, AdjType: 1, Date: vDate(time.Now())})
	}
	broadcast("debt_update", d)
	jsonOK(w, d)
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: ADJUSTMENTS
// ─────────────────────────────────────────────────────────────

func hGetAdjustments(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,COALESCE(description,''),amount,adj_type,COALESCE(created_at,NOW()) FROM adjustments ORDER BY created_at DESC NULLS LAST LIMIT 500`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Adjustment
	for rows.Next() {
		var a Adjustment
		var createdAt time.Time
		rows.Scan(&a.ID, &a.Desc, &a.Amt, &a.AdjType, &createdAt)
		a.Date = vDate(createdAt)
		list = append(list, a)
	}
	if list == nil {
		list = []Adjustment{}
	}
	jsonOK(w, list)
}

func hCreateAdjustment(w http.ResponseWriter, r *http.Request) {
	var a Adjustment
	if err := decode(r, &a); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if a.ID == "" {
		a.ID = genID("a")
	}
	claims := getClaims(r)
	now := time.Now()
	a.Date = vDate(now)
	_, err := db.Exec(r.Context(),
		`INSERT INTO adjustments(id,description,amount,adj_type,created_by,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		a.ID, a.Desc, a.Amt, a.AdjType, claims.UserID, now)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("adjustment_add", a)
	jsonOK(w, a)
}

func hDeleteAdjustment(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `DELETE FROM adjustments WHERE id=$1`, id)
	broadcast("adjustment_remove", id)
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: EXPENSES
// ─────────────────────────────────────────────────────────────

func hGetExpenses(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,name,description,amount,month,category FROM expenses ORDER BY created_at DESC`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Expense
	for rows.Next() {
		var e Expense
		rows.Scan(&e.ID, &e.Name, &e.Desc, &e.Amount, &e.Month, &e.Category)
		list = append(list, e)
	}
	if list == nil {
		list = []Expense{}
	}
	jsonOK(w, list)
}

func hCreateExpense(w http.ResponseWriter, r *http.Request) {
	var e Expense
	if err := decode(r, &e); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if e.ID == "" {
		e.ID = genID("e")
	}
	_, err := db.Exec(r.Context(),
		`INSERT INTO expenses(id,name,description,amount,month,category) VALUES($1,$2,$3,$4,$5,$6)`,
		e.ID, e.Name, e.Desc, e.Amount, e.Month, e.Category)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	broadcast("expense_add", e)
	jsonOK(w, e)
}

func hUpdateExpense(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var e Expense
	if err := decode(r, &e); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	db.Exec(r.Context(),
		`UPDATE expenses SET name=$2,description=$3,amount=$4,month=$5,category=$6,updated_at=NOW() WHERE id=$1`,
		id, e.Name, e.Desc, e.Amount, e.Month, e.Category)
	e.ID = id
	broadcast("expense_update", e)
	jsonOK(w, e)
}

func hDeleteExpense(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `DELETE FROM expenses WHERE id=$1`, id)
	broadcast("expense_remove", id)
	jsonOK(w, map[string]bool{"ok": true})
}

func hGetFunds(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,name,description,amount FROM funds ORDER BY created_at`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []Fund
	for rows.Next() {
		var f Fund
		rows.Scan(&f.ID, &f.Name, &f.Desc, &f.Amount)
		list = append(list, f)
	}
	if list == nil {
		list = []Fund{}
	}
	jsonOK(w, list)
}

func hCreateFund(w http.ResponseWriter, r *http.Request) {
	var f Fund
	if err := decode(r, &f); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if f.ID == "" {
		f.ID = genID("f")
	}
	// amount ban đầu = số dư mở quỹ
	_, err := db.Exec(r.Context(),
		`INSERT INTO funds(id,name,description,amount) VALUES($1,$2,$3,$4)`,
		f.ID, f.Name, f.Desc, f.Amount)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	// Nếu có số dư ban đầu → ghi 1 giao dịch mở quỹ
	if f.Amount != 0 {
		db.Exec(r.Context(),
			`INSERT INTO fund_txns(id,fund_id,amount,note) VALUES($1,$2,$3,$4)`,
			genID("ft"), f.ID, f.Amount, "Số dư ban đầu")
	}
	broadcast("fund_add", f)
	jsonOK(w, f)
}

func hUpdateFund(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var f Fund
	if err := decode(r, &f); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	// Chỉ sửa tên/mô tả — số dư thay đổi qua giao dịch nạp/rút
	db.Exec(r.Context(),
		`UPDATE funds SET name=$2,description=$3,updated_at=NOW() WHERE id=$1`,
		id, f.Name, f.Desc)
	f.ID = id
	db.QueryRow(r.Context(), `SELECT amount FROM funds WHERE id=$1`, id).Scan(&f.Amount)
	broadcast("fund_update", f)
	jsonOK(w, f)
}

func hDeleteFund(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `DELETE FROM fund_txns WHERE fund_id=$1`, id)
	db.Exec(r.Context(), `DELETE FROM funds WHERE id=$1`, id)
	broadcast("fund_remove", id)
	jsonOK(w, map[string]bool{"ok": true})
}

// ── Giao dịch quỹ (sổ nạp/rút) ──────────────────────────────
func hGetFundTxns(w http.ResponseWriter, r *http.Request) {
	fundID := chi.URLParam(r, "id")
	rows, err := db.Query(r.Context(),
		`SELECT id,fund_id,amount,note,created_at FROM fund_txns WHERE fund_id=$1 ORDER BY created_at DESC`, fundID)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []FundTxn
	for rows.Next() {
		var t FundTxn
		var createdAt time.Time
		rows.Scan(&t.ID, &t.FundID, &t.Amount, &t.Note, &createdAt)
		t.Date = vDate(createdAt)
		t.Time = vTime(createdAt)
		list = append(list, t)
	}
	if list == nil {
		list = []FundTxn{}
	}
	jsonOK(w, list)
}

func hCreateFundTxn(w http.ResponseWriter, r *http.Request) {
	fundID := chi.URLParam(r, "id")
	var t FundTxn
	if err := decode(r, &t); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	t.ID = genID("ft")
	t.FundID = fundID
	if _, err := db.Exec(r.Context(),
		`INSERT INTO fund_txns(id,fund_id,amount,note) VALUES($1,$2,$3,$4)`,
		t.ID, fundID, t.Amount, t.Note); err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	// Cập nhật số dư quỹ
	db.Exec(r.Context(), `UPDATE funds SET amount=amount+$2,updated_at=NOW() WHERE id=$1`, fundID, t.Amount)
	var bal int64
	db.QueryRow(r.Context(), `SELECT amount FROM funds WHERE id=$1`, fundID).Scan(&bal)
	broadcast("fund_update", Fund{ID: fundID, Amount: bal})
	jsonOK(w, t)
}

func hDeleteFundTxn(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var fundID string
	var amount int64
	if err := db.QueryRow(r.Context(), `SELECT fund_id,amount FROM fund_txns WHERE id=$1`, id).Scan(&fundID, &amount); err != nil {
		jsonErr(w, "Not found", 404)
		return
	}
	db.Exec(r.Context(), `DELETE FROM fund_txns WHERE id=$1`, id)
	db.Exec(r.Context(), `UPDATE funds SET amount=amount-$2,updated_at=NOW() WHERE id=$1`, fundID, amount)
	var bal int64
	db.QueryRow(r.Context(), `SELECT amount FROM funds WHERE id=$1`, fundID).Scan(&bal)
	broadcast("fund_update", Fund{ID: fundID, Amount: bal})
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// HANDLERS: USERS
// ─────────────────────────────────────────────────────────────

func hGetUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(r.Context(),
		`SELECT id,username,name,role,active FROM users ORDER BY id`)
	if err != nil {
		jsonErr(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var list []User
	for rows.Next() {
		var u User
		rows.Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active)
		list = append(list, u)
	}
	if list == nil {
		list = []User{}
	}
	jsonOK(w, list)
}

func hCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonErr(w, "Server error", 500)
		return
	}
	var u User
	err = db.QueryRow(r.Context(),
		`INSERT INTO users(username,password_hash,name,role) VALUES($1,$2,$3,$4) RETURNING id,username,name,role,active`,
		req.Username, string(hash), req.Name, req.Role,
	).Scan(&u.ID, &u.Username, &u.Name, &u.Role, &u.Active)
	if err != nil {
		jsonErr(w, "Username đã tồn tại", 409)
		return
	}
	jsonOK(w, u)
}

func hUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Name     string `json:"name"`
		Role     string `json:"role"`
		Active   *bool  `json:"active"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		jsonErr(w, "Invalid request", 400)
		return
	}
	if req.Password != "" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		db.Exec(r.Context(), `UPDATE users SET password_hash=$2 WHERE id=$1`, id, string(hash))
	}
	if req.Active != nil {
		db.Exec(r.Context(), `UPDATE users SET name=$2,role=$3,active=$4 WHERE id=$1`, id, req.Name, req.Role, *req.Active)
	} else {
		db.Exec(r.Context(), `UPDATE users SET name=$2,role=$3 WHERE id=$1`, id, req.Name, req.Role)
	}
	jsonOK(w, map[string]bool{"ok": true})
}

func hDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.Exec(r.Context(), `UPDATE users SET active=false WHERE id=$1`, id)
	jsonOK(w, map[string]bool{"ok": true})
}

// ─────────────────────────────────────────────────────────────
// DATABASE INIT
// ─────────────────────────────────────────────────────────────

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(50) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    name VARCHAR(100) NOT NULL,
    role VARCHAR(20) NOT NULL DEFAULT 'staff',
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS settings (
    key VARCHAR(100) PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS bida_state (
    table_id VARCHAR(10) PRIMARY KEY,
    running BOOLEAN NOT NULL DEFAULT false,
    paused BOOLEAN NOT NULL DEFAULT false,
    start_time TIMESTAMPTZ,
    paused_ms BIGINT NOT NULL DEFAULT 0,
    cart JSONB NOT NULL DEFAULT '[]',
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS tabs (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    items JSONB NOT NULL DEFAULT '[]',
    bida_id VARCHAR(10),
    bida_cost BIGINT NOT NULL DEFAULT 0,
    staff_id INT
);
ALTER TABLE tabs ADD COLUMN IF NOT EXISTS bida_cost BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(50) PRIMARY KEY,
    tab_name VARCHAR(100) NOT NULL,
    staff_name VARCHAR(100),
    bida_cost BIGINT NOT NULL DEFAULT 0,
    cart_cost BIGINT NOT NULL DEFAULT 0,
    total BIGINT NOT NULL DEFAULT 0,
    method VARCHAR(20) DEFAULT 'cash',
    details JSONB,
    paid_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS inventory (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    price BIGINT NOT NULL DEFAULT 0,
    unit VARCHAR(20) NOT NULL DEFAULT 'cái',
    cat VARCHAR(20) NOT NULL DEFAULT 'khac',
    stock INT NOT NULL DEFAULT 0,
    min_stock INT NOT NULL DEFAULT 5,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS inv_imports (
    id SERIAL PRIMARY KEY,
    product_id VARCHAR(50),
    product_name VARCHAR(100),
    qty INT NOT NULL,
    unit_cost BIGINT NOT NULL DEFAULT 0,
    total_cost BIGINT NOT NULL,
    note VARCHAR(255) DEFAULT '',
    staff_id INT,
    imported_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS debts (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    phone VARCHAR(20) DEFAULT '',
    orig_amount BIGINT NOT NULL,
    remaining BIGINT NOT NULL,
    note VARCHAR(255) DEFAULT '',
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    details JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS adjustments (
    id VARCHAR(50) PRIMARY KEY,
    description VARCHAR(255) NOT NULL,
    amount BIGINT NOT NULL,
    adj_type INT NOT NULL DEFAULT 1,
    created_by INT,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS expenses (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(255) DEFAULT '',
    amount BIGINT NOT NULL,
    month VARCHAR(7) DEFAULT '',
    category VARCHAR(50) DEFAULT 'fixed',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS funds (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description VARCHAR(255) DEFAULT '',
    amount BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS fund_txns (
    id VARCHAR(50) PRIMARY KEY,
    fund_id VARCHAR(50) NOT NULL,
    amount BIGINT NOT NULL,
    note VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_fund_txns_fund ON fund_txns(fund_id);
`

func seedDB(ctx context.Context) {
	// Admin user — password: admin123
	adminHash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)
	db.Exec(ctx,
		`INSERT INTO users(username,password_hash,name,role) VALUES('admin',$1,'Chủ quán','admin') ON CONFLICT DO NOTHING`,
		string(adminHash))

	// Staff user — password: nv123
	staffHash, _ := bcrypt.GenerateFromPassword([]byte("nv123"), bcrypt.DefaultCost)
	db.Exec(ctx,
		`INSERT INTO users(username,password_hash,name,role) VALUES('nhanvien',$1,'Minh Tâm','staff') ON CONFLICT DO NOTHING`,
		string(staffHash))

	// Bida table states
	for _, tid := range []string{"b1", "b2", "b3"} {
		db.Exec(ctx,
			`INSERT INTO bida_state(table_id,running,paused,paused_ms,cart) VALUES($1,false,false,0,'[]') ON CONFLICT DO NOTHING`, tid)
	}

	// Default settings
	settings := `{
		"bida_tables":[
			{"id":"b1","name":"Bàn phân 1","type":"phan","rateA":25000,"rateB":30000},
			{"id":"b2","name":"Bàn phân 2","type":"phan","rateA":25000,"rateB":30000},
			{"id":"b3","name":"Bàn 3C","type":"ba_bang","rateA":40000,"rateB":45000}
		],
		"cutoff":{"h":21,"m":0},
		"bank_info":{"bank":"MB Bank","account":"0123456789","name":"Thai Lai Billiards"},
		"shareholders":[
			{"name":"Hùng","pct":43},
			{"name":"Đức","pct":35},
			{"name":"Nhân","pct":22}
		],
		"fund_rate":0.05
	}`
	db.Exec(ctx, `INSERT INTO settings(key,value) VALUES('global',$1) ON CONFLICT DO NOTHING`, settings)
	// Không seed sản phẩm mẫu — sản phẩm do người dùng tự thêm qua màn Cài đặt.
}

// ─────────────────────────────────────────────────────────────
// MAIN
// ─────────────────────────────────────────────────────────────

func main() {
	initLoc()

	dbURL := env("DATABASE_URL", "postgres://bida:localpass@localhost:5432/bida?sslmode=disable")
	port := env("PORT", "3000")
	jwtSecret = []byte(env("JWT_SECRET", "bida-jwt-secret-change-in-production"))
	frontendDir := env("FRONTEND_DIR", "./frontend")

	ctx := context.Background()

	// Connect DB
	var err error
	db, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("DB connect error: %v", err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatalf("DB ping failed: %v", err)
	}
	log.Println("✓ PostgreSQL connected")

	// Run schema migration
	if _, err := db.Exec(ctx, schema); err != nil {
		log.Fatalf("Schema migration failed: %v", err)
	}
	log.Println("✓ Schema migrated")

	// Seed default data
	seedDB(ctx)
	log.Println("✓ Database seeded")

	// Start WebSocket hub
	go hub.run()

	// Router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
	}))

	// WebSocket (token in query)
	r.Get("/ws", wsHandler)

	// Public
	r.Post("/api/auth/login", hLogin)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(authMiddleware)

		r.Get("/api/me", hMe)
		r.Get("/api/settings", hGetSettings)
		r.Put("/api/settings", hPutSettings)

		r.Get("/api/bida/state", hGetBidaState)
		r.Put("/api/bida/{id}/state", hPutBidaState)

		r.Get("/api/tabs", hGetTabs)
		r.Post("/api/tabs", hCreateTab)
		r.Put("/api/tabs/{id}", hUpdateTab)
		r.Delete("/api/tabs/{id}", hDeleteTab)

		r.Get("/api/sessions", hGetSessions)
		r.Post("/api/sessions", hCreateSession)

		r.Get("/api/inventory", hGetInventory)
		r.Post("/api/inventory", hCreateProduct)
		r.Put("/api/inventory/{id}", hUpdateProduct)
		r.Delete("/api/inventory/{id}", hDeleteProduct)

		r.Get("/api/imports", hGetImports)
		r.Post("/api/imports", hCreateImport)

		r.Get("/api/debts", hGetDebts)
		r.Post("/api/debts", hCreateDebt)
		r.Post("/api/debts/{id}/pay", hPayDebt)

		r.Get("/api/adjustments", hGetAdjustments)
		r.Post("/api/adjustments", hCreateAdjustment)
		r.Delete("/api/adjustments/{id}", hDeleteAdjustment)

		r.Get("/api/expenses", hGetExpenses)
		r.Post("/api/expenses", hCreateExpense)
		r.Put("/api/expenses/{id}", hUpdateExpense)
		r.Delete("/api/expenses/{id}", hDeleteExpense)
		r.Get("/api/funds", hGetFunds)
		r.Post("/api/funds", hCreateFund)
		r.Put("/api/funds/{id}", hUpdateFund)
		r.Delete("/api/funds/{id}", hDeleteFund)
		r.Get("/api/funds/{id}/txns", hGetFundTxns)
		r.Post("/api/funds/{id}/txns", hCreateFundTxn)
		r.Delete("/api/fund-txns/{id}", hDeleteFundTxn)

		// Admin only
		r.Group(func(r chi.Router) {
			r.Use(adminOnly)
			r.Get("/api/users", hGetUsers)
			r.Post("/api/users", hCreateUser)
			r.Put("/api/users/{id}", hUpdateUser)
			r.Delete("/api/users/{id}", hDeleteUser)
		})
	})

	// Serve frontend static files
	r.Handle("/*", http.FileServer(http.Dir(frontendDir)))

	log.Printf("🎱 Bida Manager → http://0.0.0.0:%s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}
