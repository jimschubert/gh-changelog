package changelog

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sync"
	"text/template"
	"time"

	"github.com/google/go-github/v29/github"
	"golang.org/x/oauth2"

	"changelog/model"
)

const EmptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
const DefaultEnd = "HEAD"
const defaultTemplate = `
## {{.Version}}

{{range .Items -}}
* [{{.CommitHashShort}}]({{.CommitURL_}}) {{.Title}} ([{{.Author}}]({{.AuthorURL}}))
{{end}}
`

type Changelog struct {
	*model.Config
	From string
	To   string
}

type data struct {
	Version string
	Items   []model.ChangeItem
}

func newContext(c context.Context) (context.Context, context.CancelFunc) {
	timeout, cancel := context.WithTimeout(c, 30*time.Second)
	return timeout, cancel
}

func (c *Changelog) Generate(writer io.Writer) error {
	ctx := context.Background()
	token, found := os.LookupEnv("GITHUB_TOKEN")
	if !found {
		fmt.Println("Environment variable GITHUB_TOKEN not found.")
		os.Exit(1)
	}

	start := c.From
	if len(start) == 0 {
		start = EmptyTree
	}
	end := c.To
	if len(end) == 0 {
		end = DefaultEnd
	}

	// _, _ = writer.Write([]byte(fmt.Sprintf("Changelog %s..%s\n%s\n", start, end, c.Config)))

	// switch *c.Config.ResolveType {
	// case model.PullRequests:
	// 	_, _ = writer.Write([]byte("Chose PRs"))
	// case model.Commits:
	// 	fallthrough
	// default:
	// 	_, _ = writer.Write([]byte("Chose Commits"))
	// }

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)

	var client *github.Client
	if c.Config.Enterprise != nil && len(*c.Config.Enterprise) > 0 {
		c, e := github.NewEnterpriseClient(*c.Config.Enterprise, *c.Config.Enterprise, tc)
		if e != nil {
			return e
		}
		client = c
	} else {
		client = github.NewClient(tc)
	}

	compareContext, compareCancel := newContext(context.Background())
	defer compareCancel()

	// TODO: Fail if comparison is behind (example v4.0.0..v3.0.0)?
	comparison, _, compareError := client.Repositories.CompareCommits(compareContext, c.Config.Owner, c.Config.Repo, start, end)

	if compareError != nil {
		return compareError
	}

	doneChan := make(chan struct{})
	errorChan := make(chan error)
	ciChan := make(chan *model.ChangeItem)

	wg := sync.WaitGroup{}

	for _, commit := range (*comparison).Commits {
		wg.Add(1)
		go func(commit github.RepositoryCommit) {
			c.convertToChangeItem(&commit, ciChan, &wg)
		}(commit)
	}

	go func() {
		wg.Wait()
		doneChan <- struct{}{}
	}()

	all := make([]model.ChangeItem, 0)
	for {
		select {
		case e := <-errorChan:
			return e
		case ci := <-ciChan:
			// _, ciErr := writer.Write([]byte(fmt.Sprintf("%#v\n", ci)))
			// if ciErr != nil {
			// 	return ciErr
			// }

			all = append(all, *ci)
		case <-doneChan:
			// for _, ci := range all {
			// 	_, ciErr := writer.Write([]byte(fmt.Sprintf("%#v\n", &ci)))
			// 	if ciErr != nil {
			// 		return ciErr
			// 	}
			// }

			d := &data{
				Version: c.To,
				Items:   all,
			}

			var tpl = defaultTemplate
			if c.Config.Template != nil {
				b, templateErr := ioutil.ReadFile(*c.Config.Template)
				if templateErr != nil {
					_, _ = fmt.Fprintln(os.Stderr, "Unable to load template. Using default.")
				} else {
					tpl = string(b)
				}
			}

			tmpl, err := template.New("test").Parse(tpl)
			if err != nil {
				return err
			}

			_ = tmpl.Execute(writer, d)

			return nil
		}
	}
}

func (c *Changelog) convertToChangeItem(commit *github.RepositoryCommit, ch chan *model.ChangeItem, wg *sync.WaitGroup) {
	defer wg.Done()

	var t *time.Time
	if commit.GetCommit() != nil && (*commit.GetCommit()).GetAuthor() != nil && (*(*commit.GetCommit()).GetAuthor()).Date != nil {
		t = (*(*commit.GetCommit()).GetAuthor()).Date
	}

	// TODO: Groupings
	// TODO: Pull URL/Boolean
	// TODO: Excludes
	ci := &model.ChangeItem{
		Author_:        commit.Author.Login,
		AuthorURL_:     commit.Author.URL,
		CommitMessage_: commit.Commit.Message,
		Date_:          t,
		IsPull_:        nil,
		PullURL_:       nil,
		CommitHash_:    commit.SHA,
		CommitURL_:     commit.HTMLURL,
		Group_:         nil,
	}

	ch <- ci
}
