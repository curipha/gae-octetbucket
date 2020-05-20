package main

import (
  "context"
  "crypto/sha256"
  "fmt"
  "io/ioutil"
  "log"
  "mime"
  "net/http"
  "os"
  "path"
  "strings"
  "time"

  "cloud.google.com/go/datastore"
)

var projectid = os.Getenv("GOOGLE_CLOUD_PROJECT")

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

  ctx := context.Background()
  client, err := datastore.NewClient(ctx, projectid)
  if err != nil {
    log.Printf("Failed to create client: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  err = client.Get(ctx, datastore.NameKey(kind, key, nil), record)
  if err != nil {
    log.Printf("Failed to get the file from Datastore: %v", err)
    http.NotFound(w, r)
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
  w.Header().Set("Cache-Control", "public, max-age=300")
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

  ctx := context.Background()
  client, err := datastore.NewClient(ctx, projectid)
  if err != nil {
    log.Printf("Failed to create client: %v", err)
    http.Error(w, "Internal Server Error", http.StatusInternalServerError)
    return
  }

  _, err = client.Put(ctx, datastore.NameKey(kind, key, nil), record)
  if err != nil {
    log.Printf("Failed to store the file to Datastore: %v", err)
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

func main() {
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


  port := os.Getenv("PORT")
  if port == "" {
    port = "8080"
    log.Printf("Defaulting to port %s", port)
  }

  log.Printf("Listening on port %s", port)
  err := http.ListenAndServe(":" + port, nil)
  if err != nil {
    log.Fatal(err)
  }
}
