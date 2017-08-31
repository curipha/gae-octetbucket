package main

import (
  "crypto/sha256"
  "fmt"
  "io/ioutil"
  "mime"
  "net/http"
  "path"
  "strings"
  "time"

  "google.golang.org/appengine"
  "google.golang.org/appengine/datastore"
)

const kind    = "Storage"
const formarg = "file"

type Storage struct {
  Created     time.Time
  RemoteAddr  string
  UserAgent   string
  FileName    string
  ContentType string
  Size        int
  Data        []byte
}

func get(w http.ResponseWriter, r *http.Request) {
  _, file := path.Split(r.URL.Path)

  if len(file) < 1 {
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  ext := path.Ext(file)
  key := file[:len(file) - len(ext)]

  record := &Storage{}

  ctx := appengine.NewContext(r)
  err := datastore.Get(ctx, datastore.NewKey(ctx, kind, key, 0, nil), record)
  if err != nil {
    http.Error(w, "Not Found", http.StatusNotFound)
    return
  }

  ctype := mime.TypeByExtension(ext)

  switch strings.SplitN(ctype, "/", 2)[0] {
  case "text":
    ctype = "text/plain"
  case "audio", "video", "image":
    break // kept as it is
  default:
    ctype = "application/octet-stream"
  }

  w.Header().Set("Content-Type", ctype)
  w.Write(record.Data)
}

func post(w http.ResponseWriter, r *http.Request) {
  file, header, err := r.FormFile(formarg)
  if err != nil {
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  blob, err := ioutil.ReadAll(file)
  if err != nil {
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }

  fname := strings.TrimSpace(header.Filename)
  ctype := strings.TrimSpace(header.Header.Get("Content-Type"))

  record := &Storage {
    Created:     time.Now(),
    RemoteAddr:  r.RemoteAddr,
    UserAgent:   strings.TrimSpace(r.Header.Get("User-Agent")),
    FileName:    fname,
    ContentType: ctype,
    Size:        len(blob),
    Data:        blob,
  }


  key := fmt.Sprintf("%.6x", sha256.Sum256(blob)) // 12 chars

  ctx := appengine.NewContext(r)
  _, err = datastore.Put(ctx, datastore.NewKey(ctx, kind, key, 0, nil), record)
  if err != nil {
    http.Error(w, "Bad Request", http.StatusBadRequest)
    return
  }


  ext := path.Ext(fname)
  if len(ext) < 2 {
    exts, err := mime.ExtensionsByType(ctype)
    if err == nil && len(exts) > 0 {
      ext = exts[0]
    } else {
      ext = ""
    }
  }

  fmt.Fprintf(w, "https://%s/r/%s%s\n", r.Host, key, ext)
}

func init() {
  http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case "GET":
      get(w, r)
    case "POST":
      post(w, r)
    default:
      http.Error(w, "Not Implemented", http.StatusNotImplemented)
    }
  })
}
