package fastly

import (
	"fmt"
	"log"
	"strings"
	"testing"

	gofastly "github.com/fastly/go-fastly/fastly"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/terraform-plugin-sdk/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestResourceFastlyFlattenNewRelic(t *testing.T) {
	cases := []struct {
		remote []*gofastly.NewRelic
		local  []map[string]interface{}
	}{
		{
			remote: []*gofastly.NewRelic{
				{
					Version:       1,
					Name:          "newrelic-endpoint",
					Token:         "token",
					FormatVersion: 2,
				},
			},
			local: []map[string]interface{}{
				{
					"name":           "newrelic-endpoint",
					"token":          "token",
					"format_version": uint(2),
				},
			},
		},
	}

	for _, c := range cases {
		out := flattenNewRelic(c.remote)
		if diff := cmp.Diff(out, c.local); diff != "" {
			t.Fatalf("Error matching: %s", diff)
		}
	}
}

var (
	newrelicDefaultFormat = `{
  "time_elapsed":%{time.elapsed.usec}V,
  "is_tls":%{if(req.is_ssl, "true", "false")}V,
  "client_ip":"%{req.http.Fastly-Client-IP}V",
  "geo_city":"%{client.geo.city}V",
  "geo_country_code":"%{client.geo.country_code}V",
  "request":"%{req.request}V",
  "host":"%{req.http.Fastly-Orig-Host}V",
  "url":"%{json.escape(req.url)}V",
  "request_referer":"%{json.escape(req.http.Referer)}V",
  "request_user_agent":"%{json.escape(req.http.User-Agent)}V",
  "request_accept_language":"%{json.escape(req.http.Accept-Language)}V",
  "request_accept_charset":"%{json.escape(req.http.Accept-Charset)}V",
  "cache_status":"%{regsub(fastly_info.state, "^(HIT-(SYNTH)|(HITPASS|HIT|MISS|PASS|ERROR|PIPE)).*", "\2\3") }V"
}`
	// https://www.terraform.io/docs/configuration/expressions.html#string-literals
	newrelicEscapePercent          = strings.ReplaceAll(newrelicDefaultFormat, "%", "%%")
	newrelicEscapeTemplateSequence = strings.ReplaceAll(newrelicEscapePercent, "%%{", "%%%%{")
)

func TestAccFastlyServiceV1_logging_newrelic_basic(t *testing.T) {
	var service gofastly.ServiceDetail
	name := fmt.Sprintf("tf-test-%s", acctest.RandString(10))
	domain := fmt.Sprintf("fastly-test.%s.com", name)

	log1 := gofastly.NewRelic{
		Version:       1,
		Name:          "newrelic-endpoint",
		Token:         "token",
		FormatVersion: 2,
		Format:        "%h %l %u %t \"%r\" %>s %b",
	}

	log1_after_update := gofastly.NewRelic{
		Version:       1,
		Name:          "newrelic-endpoint",
		Token:         "t0k3n",
		FormatVersion: 2,
		Format:        "%h %l %u %t \"%r\" %>s %b %T",
	}

	log2 := gofastly.NewRelic{
		Version:       1,
		Name:          "another-newrelic-endpoint",
		Token:         "another-token",
		FormatVersion: 2,
		Format:        appendNewLine(newrelicDefaultFormat),
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckServiceV1Destroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceV1NewRelicConfig(name, domain),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceV1Exists("fastly_service_v1.foo", &service),
					testAccCheckFastlyServiceV1NewRelicAttributes(&service, []*gofastly.NewRelic{&log1}),
					resource.TestCheckResourceAttr(
						"fastly_service_v1.foo", "name", name),
					resource.TestCheckResourceAttr(
						"fastly_service_v1.foo", "logging_newrelic.#", "1"),
				),
			},

			{
				Config: testAccServiceV1NewRelicConfig_update(name, domain),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceV1Exists("fastly_service_v1.foo", &service),
					testAccCheckFastlyServiceV1NewRelicAttributes(&service, []*gofastly.NewRelic{&log1_after_update, &log2}),
					resource.TestCheckResourceAttr(
						"fastly_service_v1.foo", "name", name),
					resource.TestCheckResourceAttr(
						"fastly_service_v1.foo", "logging_newrelic.#", "2"),
				),
				PreventDiskCleanup: true,
			},
		},
	})
}

func testAccCheckFastlyServiceV1NewRelicAttributes(service *gofastly.ServiceDetail, newrelic []*gofastly.NewRelic) resource.TestCheckFunc {
	return func(s *terraform.State) error {

		conn := testAccProvider.Meta().(*FastlyClient).conn
		newrelicList, err := conn.ListNewRelic(&gofastly.ListNewRelicInput{
			Service: service.ID,
			Version: service.ActiveVersion.Number,
		})

		if err != nil {
			return fmt.Errorf("[ERR] Error looking up NewRelic Logging for (%s), version (%d): %s", service.Name, service.ActiveVersion.Number, err)
		}

		if len(newrelicList) != len(newrelic) {
			return fmt.Errorf("NewRelic List count mismatch, expected (%d), got (%d)", len(newrelic), len(newrelicList))
		}

		log.Printf("[DEBUG] newrelicList = %#v\n", newrelicList)

		var found int
		for _, d := range newrelic {
			for _, dl := range newrelicList {
				if d.Name == dl.Name {
					// we don't know these things ahead of time, so populate them now
					d.ServiceID = service.ID
					d.Version = service.ActiveVersion.Number
					// We don't track these, so clear them out because we also wont know
					// these ahead of time
					dl.CreatedAt = nil
					dl.UpdatedAt = nil

					if diff := cmp.Diff(d, dl); diff != "" {
						return fmt.Errorf("Bad match NewRelic logging match: %s", diff)
					}
					found++
				}
			}
		}

		if found != len(newrelic) {
			return fmt.Errorf("Error matching NewRelic Logging rules")
		}

		return nil
	}
}

func testAccServiceV1NewRelicConfig(name string, domain string) string {
	return fmt.Sprintf(`
resource "fastly_service_v1" "foo" {
  name = "%s"

  domain {
    name    = "%s"
    comment = "tf-newrelic-logging"
  }

  backend {
    address = "aws.amazon.com"
    name    = "amazon docs"
  }

  logging_newrelic {
    name   = "newrelic-endpoint"
    token  = "token"
    format = "%%h %%l %%u %%t \"%%r\" %%>s %%b"
  }

  force_destroy = true
}
`, name, domain)
}

func testAccServiceV1NewRelicConfig_update(name, domain string) string {
	return fmt.Sprintf(`
resource "fastly_service_v1" "foo" {
  name = "%s"

  domain {
    name    = "%s"
    comment = "tf-newrelic-logging"
  }

  backend {
    address = "aws.amazon.com"
    name    = "amazon docs"
  }

  logging_newrelic {
    name   = "newrelic-endpoint"
    token  = "t0k3n"
    format = "%%h %%l %%u %%t \"%%r\" %%>s %%b %%T"
  }

  logging_newrelic {
    name  = "another-newrelic-endpoint"
    token = "another-token"
		format = <<EOF
`+newrelicEscapeTemplateSequence+`
EOF
  }

  force_destroy = true
}
`, name, domain)
}
