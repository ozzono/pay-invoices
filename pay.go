package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	adb "github.com/ozzono/adbtools"
	comgas "github.com/ozzono/comgas_invoice"
	enel "github.com/ozzono/enel_invoice"
)

var (
	headless     bool
	configPath   string
	hookURL      string
	defaultSleep int
	nubank       app
	allExp       map[string]string
	logLvl       bool
)

type app struct {
	pkg      string
	activity string
}

type invoice struct {
	Provider string
	DueDate  string
	Value    string
	BarCode  string
	Status   string
}

type Enel struct {
	User enel.UserData `json:"user"`
}

type Comgas struct {
	User comgas.UserData `json:"user"`
}

type config struct {
	Enel    Enel   `json:"enel"`
	Comgas  Comgas `json:"comgas"`
	HookURL string `json:"hookURL"`
	device  adb.Device
}

func init() {
	flag.StringVar(&configPath, "config", "config.json", "Sets the path to user data in JSON format")
	flag.BoolVar(&headless, "headless", false, "Enables or disables headless chrome navigation")
	flag.BoolVar(&logLvl, "log", false, "Sets the log lvl")
	flag.IntVar(&defaultSleep, "default-sleep", 100, "Enables the default sleep time in miliseconds")
}

func main() {
	flag.Parse()
	nubank = app{
		pkg:      "com.nu.production",
		activity: "br.com.nubank.shell.screens.splash.SplashActivity",
	}

	// invoices, errs := fetchInvoices()
	// if len(invoices) == 0 {
	// 	log.Println("No invoices found")
	// 	for i := range errs {
	// 		log.Printf("err[%d]: %v", i, errs[i])
	// 	}
	// 	return
	// }

	devices, err := adb.Devices(logLvl)
	if err != nil {
		log.Println(err)
		return
	}
	if len(devices) == 0 {
		log.Println("No devices found")
		return
	}
	if len(devices) > 1 {
		log.Printf("Using device[0]: %s", devices[0].ID)
	}
	config := config{device: devices[0]}
	err = config.adbFlow([]invoice{})
	if err != nil {
		log.Println(err)
		return
	}
}

func (c *config) adbFlow(invoices []invoice) error {
	log.Println("Starting adbFlow")
	allExp = allExpressions()
	c.device.ScreenSize()
	if !c.device.IsScreenON() {
		c.device.WakeUp()
		c.device.Swipe([4]int{int(c.device.Screen.Width / 2), c.device.Screen.Height - 100, int(c.device.Screen.Width / 2), 100})
	}
	c.device.CloseApp(nubank.pkg)
	err := c.device.StartApp(nubank.pkg, nubank.activity, "")
	if err != nil {
		return fmt.Errorf("StartApp err: %v", err)
	}
	err = c.device.WaitInScreen(10, "Olá")
	if err != nil {
		return err
	}
	err = c.payFlow()
	if err != nil {
		return err
	}

	return nil
}

func (c *config) payFlow() error {
	log.Println("Starting payFlow")
	err := c.exp2tap(allExp["buttonRow"])
	if err != nil {
		return err
	}

	err = c.device.WaitInScreen(10, "Pagar um boleto")
	if err != nil {
		return err
	}

	err = c.exp2tap(allExp["invoiceButton"])
	if err != nil {
		return err
	}
	return nil
}

func enelInvoice(user enel.UserData) (invoice, error) {
	f := enel.NewFlow(headless)
	f.User = user
	invoiceData, err := f.InvoiceFlow()
	if err != nil {
		log.Printf("f.InvoiceFlow err: %v", err)
		return invoice{}, err
	}
	return invoice{
		Provider: "enel",
		DueDate:  invoiceData.DueDate,
		Value:    invoiceData.Value,
		BarCode:  invoiceData.BarCode,
		Status:   invoiceData.Status,
	}, nil
}

func comgasInvoice(user comgas.UserData) (invoice, error) {
	f := comgas.NewFlow(headless)
	f.User = user
	invoiceData, err := f.InvoiceFlow()
	if err != nil {
		log.Printf("f.InvoiceFlow err: %v", err)
		return invoice{}, err
	}
	return invoice{
		Provider: "comgas",
		DueDate:  invoiceData.DueDate,
		Value:    invoiceData.Value,
		BarCode:  invoiceData.BarCode,
		Status:   invoiceData.Status,
	}, nil
}

func setConfig() (config, error) {
	if len(configPath) == 0 {
		return config{}, fmt.Errorf("invalid path; cannot be empty")
	}
	jsonFile, err := ioutil.ReadFile(configPath)
	if err != nil {
		return config{}, err
	}
	config := config{}
	err = json.Unmarshal(jsonFile, &config)
	return config, err
}

func (invoice *invoice) toText() string {
	return fmt.Sprintf(
		"%s successfully captured:\nDueDate: %s\nValue: %s\nStatus: %s",
		invoice.Provider,
		invoice.DueDate,
		invoice.Value,
		invoice.Status,
	)
}

func slackMsg(body, hookURL string) error {
	log.Println("Sending slack message")
	payload := strings.NewReader(fmt.Sprintf("{\"text\":\"%s\"}", body))

	req, err := http.NewRequest("POST", hookURL, payload)
	if err != nil {
		return err
	}

	req.Header.Add("content-type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("response statusCode: %v\nres: %v", res.StatusCode, res)
	}
	return nil
}

func fetchInvoices() ([]invoice, []error) {
	invoices := []invoice{}
	errs := []error{}
	config, err := setConfig()
	if err != nil {
		return []invoice{}, []error{err}
	}
	output, err := comgasInvoice(config.Comgas.User)
	if err != nil {
		log.Printf("comgasInvoice err: %v", err)
	} else {
		invoices = append(invoices, output)
	}

	output, err = enelInvoice(config.Enel.User)
	if err != nil {
		log.Printf("enelInvoice err: %v", err)
		errs = append(errs, err)
	} else {
		invoices = append(invoices, output)
	}

	log.Printf("%#v", invoices)
	return invoices, errs
}

func (c *config) exp2tap(exp string) error {
	xmlScreen, err := c.device.XMLScreen(true)
	if err != nil {
		return err
	}
	coords, err := adb.XMLtoCoords(applyRegexp(exp, xmlScreen)[1])
	if err != nil {
		return err
	}
	c.device.TapScreen(coords[0], coords[1], 10)
	return nil
}

func sleep(delay int) {
	time.Sleep(time.Duration(delay*defaultSleep) * time.Millisecond)
}

func matchExp(exp, text string) []string {
	re := regexp.MustCompile(exp)
	match := re.FindStringSubmatch(text)
	if len(match) < 1 {
		log.Printf("Unable to find match for exp %s\n", exp)
		return []string{}
	}
	return match
}

func match(exp, text string) bool {
	return regexp.MustCompile(exp).MatchString(text)
}

func allExpressions() map[string]string {
	return map[string]string{
		"buttonRow":     "Pagar.+?(\\[\\d+,\\d+\\]\\[\\d+,\\d+\\])",
		"invoiceButton": "Pagar.um.boleto\".*?(\\[\\d+,\\d+\\]\\[\\d+,\\d+\\])",
	}
}

func applyRegexp(exp, text string) []string {
	re := regexp.MustCompile(exp)
	matches := re.FindStringSubmatch(text)
	if len(matches) < 1 {
		log.Printf("unable to find match for exp %s\n", exp)
		return []string{}
	}
	return matches
}
