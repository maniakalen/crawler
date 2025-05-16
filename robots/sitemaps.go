package robots

import (
	"encoding/xml"
	"io"
	"net/http"
	"time"
)

type SitemapIndex struct {
	URL      string
	XMLName  xml.Name  `xml:"sitemapindex"`
	Sitemaps []Sitemap `xml:"sitemap"`
}

type Sitemap struct {
	Loc     string    `xml:"loc"`
	Lastmod time.Time `xml:"lastmod"`
	URLSet  URLSet
}

type URLSet struct {
	URL     string
	XMLName xml.Name `xml:"urlset"`
	URLs    []URL    `xml:"url"`
}

type URL struct {
	Loc     string    `xml:"loc"`
	Lastmod time.Time `xml:"lastmod"`
}

type SitemapInterface interface {
	Process() bool
	GetURLs() []string
}

func NewSitemapIndex(link string) *SitemapIndex {
	return &SitemapIndex{URL: link}
}

func (s *SitemapIndex) Process() bool {
	resp, err := http.Get(s.URL)
	if err != nil {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	err = xml.Unmarshal(body, s)
	return err == nil
}

func (s *SitemapIndex) GetURLs() []URL {
	urls := []URL{}
	for _, smap := range s.Sitemaps {
		if time.Now().Unix() > (smap.Lastmod.Unix() + 86400) {
			smap.URLSet.URL = smap.Loc
			urls = append(urls, smap.URLSet.GetURLs()...)
		}
	}
	return urls
}

func NewURLSet(link string) *URLSet {
	return &URLSet{URL: link}
}

func (s *URLSet) Process() bool {
	resp, err := http.Get(s.URL)
	if err != nil {
		return false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}
	err = xml.Unmarshal(body, s)
	return err == nil
}

func (s *URLSet) GetURLs() []URL {
	if len(s.URLs) == 0 {

	}
	return s.URLs
}

func ExtractURLs(smap string) []URL {
	urls := make([]URL, 0)
	sitemap := NewSitemapIndex(smap)
	if sitemap.Process() {
		urls = append(urls, sitemap.GetURLs()...)
	} else {
		sitemap := NewURLSet(smap)
		if sitemap.Process() {
			urls = append(urls, sitemap.GetURLs()...)
		}
	}
	return urls
}
