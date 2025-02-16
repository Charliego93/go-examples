package zb

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charliego93/websocket"
	logger "github.com/charmbracelet/log"
	json "github.com/json-iterator/go"
	"github.com/shopspring/decimal"
	"github.com/spf13/cast"
	"github.com/stretchr/objx"
	"github.com/whimthen/temp/zb/autoapi"
	"gopkg.in/yaml.v3"
)

const (
	EventAddChannel = "addChannel"

	ChannelIncrRecord = "push_user_incr_record"
)

type User struct {
	Name      string `yaml:"name"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`

	ch chan objx.Map
	ac *autoapi.Account
}

type Config struct {
	ApiURL   string        `yaml:"apiURL"`
	TradeRUL string        `yaml:"tradeRUL"`
	KlineURL string        `yaml:"klineURL"`
	WsapiURL string        `yaml:"wsapiURL"`
	Interval time.Duration `yaml:"interval"`
	Markets  []string      `yaml:"markets"`
	Users    []User        `yaml:"accounts"`
}

type Ticker struct {
	Date   string `json:"date"`
	Ticker struct {
		High     decimal.Decimal `json:"high"`
		Vol      decimal.Decimal `json:"vol"`
		Last     decimal.Decimal `json:"last"`
		Low      decimal.Decimal `json:"low"`
		Buy      decimal.Decimal `json:"buy"`
		Sell     decimal.Decimal `json:"sell"`
		Turnover decimal.Decimal `json:"turnover"`
		Open     decimal.Decimal `json:"open"`
		RiseRate decimal.Decimal `json:"riseRate"`
	}
}

type Depth struct {
	Asks      [][]decimal.Decimal `json:"asks"`
	Bids      [][]decimal.Decimal `json:"bids"`
	Timestamp int64               `json:"timestamp"`
}

type MarketConfig struct {
	AmountScale decimal.Decimal `json:"amountScale"`
	MinAmount   decimal.Decimal `json:"minAmount"`
	MinSize     decimal.Decimal `json:"minSize"`
	PriceScale  decimal.Decimal `json:"priceScale"`
}

type TradeProcessor struct {
	logger *logger.Logger
	user   User
}

func (p *TradeProcessor) SetLogger(l *logger.Logger) {
	p.logger = l
}

func (p *TradeProcessor) OnReceive(frame *websocket.Frame) {
	buf, err := io.ReadAll(frame.Reader)
	if err != nil {
		p.logger.Errorf("读取响应失败: %v", err)
		return
	}

	content := string(buf)
	if !strings.HasPrefix(content, "{") || !strings.HasSuffix(content, "}") {
		p.logger.Warnf("收到非 JSON 类型的消息: %d, Msg: %s", frame.Type, buf)
		return
	}

	obj, err := objx.FromJSON(content)
	if err != nil {
		p.logger.Errorf("收到消息反序列化失败: %+v", err)
		return
	}

	dataType := obj.Get("dataType").String()
	switch dataType {
	case "quickDepth":
		p.logger.Debugf("收到快速行情: %s", content)
		cprice.Store(decimal.NewFromFloat(obj.Get("currentPrice").Float64()))

	case "userIncrRecord":
		p.user.ch <- obj
	default:
		p.logger.Warnf("收到未处理的消息类型: %s", content)
	}
}

func receiveOrder(ctx context.Context, logger *logger.Logger, user User) {
	for {
		select {
		case <-ctx.Done():
			logger.Infof("服务已停止")
		case obj := <-user.ch:
			record := obj.Get("record").InterSlice()
			entrustId := record[0].(string)
			unitPrice := decimal.NewFromFloat(record[1].(float64))
			numbers := decimal.NewFromFloat(record[2].(float64))
			completeNumbers := decimal.NewFromFloat(record[3].(float64))
			types := cast.ToInt(record[5])

			if numbers.Equal(completeNumbers) {
				logger.Infof("本次成交: [%s:%d] = %s / %s", entrustId, types, unitPrice, numbers)
				if !*rOrder {
					return
				}

				var opUsers []User
				for _, u := range config.Users {
					if u.Name == user.Name {
						continue
					}

					opUsers = append(opUsers, u)
				}

				if len(opUsers) == 0 {
					logger.Errorf("没有对手用户, 使用自成交模式")
					opUsers = append(opUsers, user)
				}

				opu := opUsers[rand.Intn(len(opUsers))]
				market := strings.TrimSuffix(obj.Get("market").String(), "default")
				autoapi.Order(market, unitPrice, numbers, autoapi.ReverseTradeType(types), autoapi.WithAccount(opu.ac))
				return
			}

			logger.Debugf("收到订单: ID: %s, Price: %s, Numbers: %s - %s, Types: %d",
				entrustId, unitPrice, numbers, completeNumbers, types)
		}
	}
}

type Websocket struct {
	*websocket.Client
	logger *logger.Logger
	user   User
	err    error
}

func NewWebsocket(ctx context.Context, user User) *Websocket {
	prefix := user.Name + "*" + config.WsapiURL
	log := logger.NewWithOptions(os.Stdout, logger.Options{
		Prefix:          prefix,
		ReportTimestamp: true,
		TimeFormat:      time.DateTime,
	})
	client := websocket.NewClient(
		ctx, config.WsapiURL, &TradeProcessor{user: user},
		websocket.WithPing(websocket.NewStringMessage("ping")),
		websocket.WithLogger(log),
	)
	err := client.Connect()
	if err != nil {
		panic(err)
	}

	return &Websocket{
		Client: client,
		logger: log,
		user:   user,
	}
}

func (w *Websocket) SubscribeRecord(markets ...string) *Websocket {
	if w.err != nil {
		return w
	}

	for _, market := range markets {
		market := market
		w.err = w.Client.Subscribe((&RequestMsg{
			Event:     EventAddChannel,
			Channel:   ChannelIncrRecord,
			AccessKey: w.user.AccessKey,
			Market:    &market,
		}).Signed(w.user.SecretKey))
		if w.err != nil {
			panic(w.err)
		}
	}
	return w
}

var (
	config  Config
	rOrder  = flag.Bool("rOrder", false, "成交后是否下 taker 吃单")
	wss     = flag.Bool("wss", false, "是否开启 wss")
	buyer   atomic.Value
	seller  atomic.Value
	cprice  atomic.Value
	markets map[string]MarketConfig
)

func init() {
	buf, err := os.ReadFile("config.yml")
	if err != nil {
		panic(err)
	}

	err = yaml.Unmarshal(buf, &config)
	if err != nil {
		panic(err)
	}

	rand.Seed(time.Now().UnixMilli())
	initMarketConfig()
}

func listenQuickDepth(ctx context.Context) {
	group := getGroupMarkets()
	logger.Debugf("GroupMarkets: %+v", group)
	zoneRegex := regexp.MustCompile("(" + strings.Join(group["zone"], "|") + ")$")

	clients := make(map[string]*websocket.Client)

	for _, market := range config.Markets {
		websocketURL := config.WsapiURL
		if !strings.HasSuffix(websocketURL, "/") {
			websocketURL += "/"
		}
		websocketURL += zoneRegex.ReplaceAllString(market, "")

		var client *websocket.Client
		if c, ok := clients[websocketURL]; ok {
			client = c
		} else {
			c := websocket.NewClient(
				ctx, websocketURL, &TradeProcessor{},
				websocket.WithPing(websocket.NewStringMessage("ping")),
			)
			err := c.Connect()
			if err != nil {
				panic(err)
			}
			client = c
			clients[websocketURL] = client
		}

		err := client.Subscribe(&RequestMsg{
			Event:   EventAddChannel,
			Channel: market + "_quick_depth",
		})
		if err != nil {
			logger.Fatalf("订阅深度失败: %+v", err)
		}
	}
}

func TestAutoTrade(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	if *wss {
		listenQuickDepth(ctx)
	}

	for _, user := range config.Users {
		user.ch = make(chan objx.Map, 10)
		user.ac = &autoapi.Account{
			Account:   user.Name,
			AccessKey: user.AccessKey,
			SecretKey: user.SecretKey,
			API:       config.ApiURL,
			Trade:     config.TradeRUL,
			KLine:     config.KlineURL,
			WSAPI:     config.WsapiURL,
		}

		if *wss {
			client := NewWebsocket(ctx, user).
				SubscribeRecord(config.Markets...)

			go receiveOrder(ctx, client.logger, user)
		}

		for _, market := range config.Markets {
			go makeOrder(ctx, user, market)
		}
	}

	select {
	case <-ctx.Done():
		cancel()
	}
}

func makeOrder(ctx context.Context, user User, market string) {
	log := logger.NewWithOptions(os.Stdout, logger.Options{
		Prefix:          fmt.Sprintf("ORDER:%s:%s", strings.ToUpper(market), user.Name),
		ReportTimestamp: true,
		TimeFormat:      time.DateTime,
	})
	ticker := time.NewTicker(config.Interval)
	for {
		select {
		case <-ticker.C:
			ticker, err := getTicker(market)
			if err != nil {
				log.Errorf("获取 Ticker 失败, 休眠1min: %s = %s", market, err)
				time.Sleep(time.Minute)
				continue
			}

			types := rand.Intn(2)
			tradeType := autoapi.TradeTypeByInt(types)
			var numbers, price decimal.Decimal // numbers = minNumber * 2
			sub := ticker.Ticker.Sell.Sub(ticker.Ticker.Buy)
			exponent := sub.Exponent()
			randN := rand.Int63n(sub.CoefficientInt64())
			price = randomPrice(market)
			if price.IsZero() {
				price = ticker.Ticker.Buy.Add(decimal.NewFromInt(randN).Shift(exponent))
			}

			if types&1 == 1 { // buy
				upper := ticker.Ticker.Last.Mul(decimal.NewFromFloat(1.5))
				if price.GreaterThan(upper) {
					price = upper
				}
			} else {
				lower := ticker.Ticker.Last.Mul(decimal.NewFromFloat(0.5))
				if price.LessThan(lower) {
					price = lower
				}
			}

			if conf, ok := markets[market]; ok {
				total := conf.MinAmount.Mul(price)
				if total.LessThan(conf.MinAmount) {
					numbers = conf.MinSize.Div(price).Ceil()
				} else {
					numbers = conf.MinAmount
				}
			} else {
				numbers = decimal.NewFromInt(1)
			}

			resp := autoapi.Order(market, price, numbers, tradeType, autoapi.WithAccount(user.ac))
			if resp.Code == 1000 {
				log.Infof("下单成功: Numbers: %s, Price: %s, TradeType: %s",
					numbers, price, tradeType.String())
			} else {
				log.Errorf("下单失败: Numbers: %s, Price: %s, TradeType: %s, Reason: %s",
					numbers, price, tradeType.String(), resp.Message)
			}
		case <-ctx.Done():
			log.Infof("退出布单流程....")
			return
		}
	}
}

func randomPrice(market string) decimal.Decimal {
	resp, err := http.Get(config.ApiURL + fmt.Sprintf("data/v1/depth?market=%s&size=3", market))
	if err != nil {
		return decimal.Zero
	}

	var depth Depth
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&depth)
	if err != nil {
		return decimal.Zero
	}

	var prices []decimal.Decimal
	for _, ask := range depth.Asks {
		prices = append(prices, ask[0])
	}
	for _, bind := range depth.Bids {
		prices = append(prices, bind[0])
	}
	return prices[rand.Intn(len(prices))]
}

func initMarketConfig() {
	resp, err := http.Get(config.ApiURL + "data/v1/markets")
	if err != nil {
		panic(err)
	}

	configs := make(map[string]MarketConfig)
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&configs)
	if err != nil {
		panic(err)
	}

	markets = make(map[string]MarketConfig)
	for k, v := range configs {
		markets[strings.ReplaceAll(k, "_", "")] = v
	}
}

func getTicker(market string) (Ticker, error) {
	resp, err := http.Get(config.ApiURL + "data/v1/ticker?market=" + market)
	if err != nil {
		return Ticker{}, err
	}

	var ticker Ticker
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&ticker)
	return ticker, err
}

func getGroupMarkets() map[string][]string {
	resp, err := http.Get(config.ApiURL + "data/v1/getGroupMarkets")
	if err != nil {
		logger.Fatalf("获取 GroupMarkets 失败: %+v", err)
	}

	group := make(map[string][]string)
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&group)
	if err != nil {
		logger.Fatalf("GroupMarkets 反序列化失败: %s", err)
	}
	return group
}

func TestDecimal(t *testing.T) {
	market := "trxusdt"
	ticker, err := getTicker(market)
	if err != nil {
		t.Fatal(err)
	}

	sub := ticker.Ticker.Sell.Sub(ticker.Ticker.Buy)
	exponent := sub.Exponent()
	randN := rand.Int63n(sub.CoefficientInt64())
	price := ticker.Ticker.Buy.Add(decimal.NewFromInt(randN).Shift(exponent))
	t.Logf("tb: %s, ts: %s, price: %s", ticker.Ticker.Buy, ticker.Ticker.Sell, price)
}

func TestOrder(t *testing.T) {
	resp := autoapi.Order(
		"ethusdt",
		decimal.NewFromFloat(1600),
		decimal.NewFromFloat(0.01),
		autoapi.TradeTypeBuy,
		autoapi.WithAccount(getAccount(0)),
	)

	t.Logf("Order Response: %+v", resp)
}

func TestCancelAllOrders(t *testing.T) {
	for idx := range config.Users {
		account := getAccount(idx)
		ethusdt := autoapi.CancelAllOrders("ethusdt", autoapi.WithAccount(account))
		btcusdt := autoapi.CancelAllOrders("btcusdt", autoapi.WithAccount(account))
		logger.Infof("ETHUSDT: %+v, BTCUSDT: %+v", ethusdt, btcusdt)
	}
}

func getAccount(idx int) *autoapi.Account {
	return &autoapi.Account{
		Account:   config.Users[idx].Name,
		AccessKey: config.Users[idx].AccessKey,
		SecretKey: config.Users[idx].SecretKey,
		API:       config.ApiURL,
		Trade:     config.TradeRUL,
		KLine:     config.KlineURL,
		WSAPI:     config.WsapiURL,
	}
}

func TestOrderMoreV2(t *testing.T) {
	account := getAccount(0)
	autoapi.BatchOrder("ethusdt", autoapi.TradeTypeBuy, [][]decimal.Decimal{
		{decimal.NewFromFloat(1630), decimal.NewFromFloat(0.01)},
		{decimal.NewFromFloat(1620), decimal.NewFromFloat(0.01)},
	}, autoapi.WithAccount(account))
}

type TradeHistory struct {
	logger *logger.Logger
}

func (t *TradeHistory) OnReceive(frame *websocket.Frame) {
	if frame == nil || frame.Reader == nil {
		t.logger.Error("websoclet received frame is nil...")
		return
	}
	bs, err := io.ReadAll(frame.Reader)
	if err != nil {
		t.logger.Error("读取数据出错", err)
		return
	}
	t.logger.Infof("收到成交数据: %s", bs)
}

func (t *TradeHistory) SetLogger(logger *logger.Logger) {
	t.logger = logger
}

func TestTicker(t *testing.T) {
	client := websocket.NewClient(
		context.Background(),
		"wss://api.bw6.com/websocket",
		// "ws://ttapi2.100-130.net/websocket",
		&TradeHistory{},
		websocket.WithPing(websocket.NewStringMessage("ping")),
	)

	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}

	if err := client.Subscribe(struct {
		*websocket.JsonMessage
		Event   string `json:"event"`
		Channel string `json:"channel"`
	}{
		Event:   "addChannel",
		Channel: "btcusdt_trades",
	}); err != nil {
		t.Fatal(err)
	}

	<-make(chan struct{})
}
