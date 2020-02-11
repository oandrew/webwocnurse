package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/hyacinthus/mp3join"
	cli "github.com/urfave/cli/v2"
)

type Downloader struct {
	TotaraSession string
}

var client = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if strings.HasSuffix(req.URL.String(), "login/index.php") {
			return errors.New("bad cookie")
		}
		return nil
	},
}

var debug = false

func (d *Downloader) downloadStream(url string) io.ReadCloser {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		panic(err)
	}
	req.AddCookie(&http.Cookie{
		Name:  "TotaraSessionprod",
		Value: d.TotaraSession,
	})

	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	if debug {
		log.Printf("%s: %v", url, resp.StatusCode)
	}
	return resp.Body

}

func (d *Downloader) download(url string) string {
	r := d.downloadStream(url)
	data, _ := ioutil.ReadAll(r)
	// if debug {
	// 	log.Printf("%s\n%s", url, string(data))
	// }
	return string(data)
}

func processMain(body string) []string {
	re := regexp.MustCompile(`https://learn.*?scorm/view.php\?id=\d+`)

	return re.FindAllString(body, -1)
}

type Scorm struct {
	Currentorg string
	Sco        string
	Scorm      string
}

func processScorm(body string) Scorm {
	re := regexp.MustCompile(`var scormplayerdata =(.*?);`)
	matches := re.FindStringSubmatch(body)

	//fmt.Println(matches[1])

	var scorm Scorm
	json.Unmarshal([]byte(matches[1]), &scorm)

	return scorm
}

func findBaseUrl(content string) string {
	re := regexp.MustCompile(`https?://.*?pluginfile.*?.html`)
	s := re.FindString(content)
	u, _ := url.Parse(s)
	u.Path = path.Dir(u.Path)
	return u.String()
}

func (d *Downloader) downloadAudio(scorm Scorm) *bytes.Reader {
	url := fmt.Sprintf(`https://learn.webwocnurse.com/mod/scorm/loadSCO.php?a=%s&scoid=%s&currentorg=&mode=&attempt=1`, scorm.Scorm, scorm.Sco)

	bdir := findBaseUrl(d.download(url))

	//fmt.Println(bdir)

	dataUrl := fmt.Sprintf(`%s/html5/data/js/data.js`, bdir)
	dataBody := d.download(dataUrl)

	audioRe := regexp.MustCompile(`story_content/\w+\.mp3`)

	joiner := mp3join.New()

	for _, a := range audioRe.FindAllString(dataBody, -1) {
		audioUrl := fmt.Sprintf(`%s/%s`, bdir, a)
		audioStream := d.downloadStream(audioUrl)
		joiner.Append(audioStream)
		audioStream.Close()
	}

	return joiner.Reader()
}

func (d *Downloader) processCourse(id string, f func(scorm Scorm)) {
	mainBody := d.download(fmt.Sprintf("https://learn.webwocnurse.com/course/view.php?id=%s", id))

	scormUrls := processMain(mainBody)
	log.Printf("Found %d audios in the course", len(scormUrls))
	var wg sync.WaitGroup
	wg.Add(len(scormUrls))
	for _, scormUrl := range scormUrls {
		go func(scormUrl string) {
			defer wg.Done()
			scormBody := d.download(scormUrl)
			scorm := processScorm(scormBody)
			f(scorm)

		}(scormUrl)
	}
	wg.Wait()
}

func (d *Downloader) downloadCourse(id string) {
	log.Printf("Downloading course %s to current directory", id)

	d.processCourse(id, func(scorm Scorm) {
		outName := fmt.Sprintf("%s.mp3", scorm.Currentorg)
		if _, err := os.Stat(outName); !os.IsNotExist(err) {
			fmt.Printf("Skipping %s: file already exist\n", outName)
			return
		}
		f, _ := os.Create(outName)
		log.Printf("Downloading %s", scorm.Currentorg)
		outMp3 := d.downloadAudio(scorm)
		io.Copy(f, outMp3)
		f.Close()

		log.Printf("Done! %s", scorm.Currentorg)
	})
}

func (d *Downloader) listCourse(id string) {
	d.processCourse(id, func(scorm Scorm) {
		fmt.Println(scorm.Currentorg)
	})
}

func main() {
	app := &cli.App{
		Name: "webwocnurse",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "cookie",
				Aliases:  []string{"c"},
				Usage:    "Value of TotaraSessionprod",
				Required: true,
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "debug",
			},
		},
		Before: func(c *cli.Context) error {
			if c.Bool("debug") {
				debug = true
			}
			return nil
		},
		Commands: []*cli.Command{
			{
				Name:      "list",
				ArgsUsage: "<course-id>",
				Usage:     "list all audio for a course ",
				Action: func(c *cli.Context) error {
					id := c.Args().First()
					if id == "" {
						return errors.New("no course id specified")
					}
					d := Downloader{
						TotaraSession: c.String("cookie"),
					}
					d.listCourse(id)
					return nil
				},
			},
			{
				Name:      "download",
				ArgsUsage: "<course-id>",
				Aliases:   []string{"a"},
				Usage:     "download all  audios for a course",
				Action: func(c *cli.Context) error {
					id := c.Args().First()
					if id == "" {
						return errors.New("no course id specified")
					}
					d := Downloader{
						TotaraSession: c.String("cookie"),
					}
					d.downloadCourse(id)
					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		fmt.Println()
		fmt.Println(err)
	}
}
