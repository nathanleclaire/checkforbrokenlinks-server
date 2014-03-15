package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/dpapathanasiou/go-recaptcha"
	"io/ioutil"
	"log"
	"net/http"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

type ParsedResponse struct {
	Success bool     `json:"success"`
	Links   []map[string]string `json:"links"`
}

type EmailUser struct {
	Username    string
	Password    string
	EmailServer string
	Port        int
}

type SmtpTemplateData struct {
	From    string
	To      string
	Subject string
	Body    string
}

type Configuration struct {
	Smtp                EmailUser
	RecaptchaPrivateKey string
}

type ReCaptcha struct {
	Response  string
	Challenge string
}

type SendEmailData struct {
	YourEmail string
	YourName  string
	Feedback  string
	Captcha   ReCaptcha
}

var conf Configuration

func sendMail(emailUser EmailUser, auth smtp.Auth, from string, to string, subject string, body string) error {
	const emailTemplate = `From: {{.From}}
To: {{.To}} 
Subject: {{.Subject}}

{{.Body}}

Sincerely,

{{.From}}
`
	var err error
	var doc bytes.Buffer
	context := &SmtpTemplateData{from, to, subject, body}
	t := template.New("emailTemplate")
	if t, err = t.Parse(emailTemplate); err != nil {
		log.Print("error trying to parse mail template ", err)
	}
	if err = t.Execute(&doc, context); err != nil {
		log.Print("error trying to execute mail template ", err)
	}
	err = smtp.SendMail(emailUser.EmailServer+":"+strconv.Itoa(emailUser.Port),
		auth,
		emailUser.Username,
		[]string{"nathanleclaire@gmail.com"},
		doc.Bytes())
	if err != nil {
		log.Print("ERROR: attempting to send a mail ", err)
	}

	return nil
}

func readConfigurationFile(filepath string) Configuration {
	var conf Configuration
	if rawConfigurationJson, err := ioutil.ReadFile(filepath); err != nil {
		log.Print("error reading smtp config file ", err)
	}
	if err = json.Unmarshal(rawConfigurationJson, &conf); err != nil {
		log.Print("error unmarshalling config ", err)
	}
	return conf
}

func connectToSmtpServer(emailUser EmailUser) smtp.Auth {
	auth := smtp.PlainAuth("", emailUser.Username, emailUser.Password, emailUser.EmailServer)
	return auth
}

func getFailedSlurpResponse() []byte {
	failResponse := &ParsedResponse{false, nil}
	if failResponseJSON, err := json.Marshal(failResponse); err != nil {
		log.Print("something went really weird in attempt to marshal a fail json ", err)
		failResponseJSON, _ = json.Marshal(nil)
	}
	return failResponseJSON
}

func slurpHandler(w http.ResponseWriter, r *http.Request) {
	urlToScrape := strings.ToLower(r.URL.Query().Get("urlToScrape"))

	var doc *goquery.Document
	var e error
	var parsedResponseJSON []byte

	var link map[string]string
	var href string
	var exists bool
	var content string

	var links []map[string]string

	if doc, e = goquery.NewDocument(urlToScrape); e == nil {
		if crossDomainRegex, err := regexp.Compile(`^http`); err != nil {
			log.Printf("issue compiling regular expression to validate cross domain URLs")
		}

		doc.Find("a").Each(func(i int, selection *goquery.Selection) {
			link = make(map[string]string)
			if href, exists = selection.Attr("href"); !exists {
				log.Print("href does not exist for: ", selection)
				href = ""
			}
			content = selection.Contents().Text()
			if !crossDomainRegex.Match([]byte(href)) {
				href = urlToScrape+href // same origin link
			}
			link["href"] = href
			link["content"] = content
			links = append(links, link)
		})
		parsedResponse := &ParsedResponse{true, links}
		if parsedResponseJSON, err = json.Marshal(parsedResponse); err != nil {
			parsedResponseJSON = getFailedSlurpResponse()
		}
	} else {
		log.Print("error querying for document: ", urlToScrape, "err : ", e)
		parsedResponseJSON = getFailedSlurpResponse()
	}

	w.Write(parsedResponseJSON)

}

func checkHandler(w http.ResponseWriter, r *http.Request) {
	urlToCheck := r.URL.Query().Get("urlToCheck")
	if externalServerResponse, err := http.Get(urlToCheck); err != nil {
		log.Print("error getting in checkHandler ", urlToCheck)
	}

	response := map[string]interface{}{
		"status":     externalServerResponse.Status,
		"statusCode": externalServerResponse.StatusCode,
	}

	if responseJSON, err := json.Marshal(response); err != nil {
		log.Print("error Marshalling check response json: ", err)
	}
	w.Write(responseJSON)
}

func emailHandlerClosure(auth smtp.Auth, recaptchaPrivateKey string, emailUser EmailUser) http.HandlerFunc {
	// Use a closure so we can pass auth to the handler
	return (func(w http.ResponseWriter, r *http.Request) {

		var jsonResponse []byte
		var err error
		response := map[string]interface{}{
			"success": true,
		}
		dec := json.NewDecoder(r.Body)
		contactData := SendEmailData{}
		if dec.Decode(&contactData) != nil {
			log.Print(err)
			response["success"] = false
		}

		// call the recaptcha server
		recaptcha.Init(recaptchaPrivateKey)
		captchaIsValid := recaptcha.Confirm(r.RemoteAddr, contactData.Captcha.Challenge, contactData.Captcha.Response)

		if captchaIsValid {
			go sendMail(emailUser,
				auth,
				contactData.YourName+fmt.Sprintf(" <%s>", contactData.YourEmail),
				"Nathan LeClaire <nathan.leclaire@gmail.com>",
				"CFBL Feedback from "+contactData.YourName,
				contactData.Feedback)
		} else {
			response["success"] = false
		}
		if jsonResponse, err = json.Marshal(response); err != nil {
			log.Print("marshalling r ", err)
		}
		w.Write(jsonResponse)
	})
}

func main() {
	conf = readConfigurationFile("../conf/conf.json")
	auth := connectToSmtpServer(conf.Smtp)
	http.HandleFunc("/slurp", slurpHandler)
	http.HandleFunc("/check", checkHandler)
	http.HandleFunc("/email", emailHandlerClosure(auth, conf.RecaptchaPrivateKey, conf.Smtp))
	http.Handle("/", http.FileServer(http.Dir("..")))
	http.Handle("/css/", http.FileServer(http.Dir("..")))
	http.Handle("/img/", http.FileServer(http.Dir("..")))
	http.Handle("/lib/", http.FileServer(http.Dir("..")))
	http.Handle("/partials/", http.FileServer(http.Dir("..")))
	http.Handle("/js/", http.FileServer(http.Dir("..")))
	if http.ListenAndServe(":8000", nil) != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
