package main

import (
  "bytes"
  "fmt"
  "io/ioutil"
  "mime/multipart"
  "net/http"
  "os"
  "path"
)

const endpoint = "https://octetbucket.appspot.com/upload"
const formarg  = "file"

func main() {
  if len(os.Args) != 2 {
    fmt.Fprintf(os.Stderr, "%s: missing file operand\n", path.Base(os.Args[0]))
    os.Exit(1)
  }


  fpath := os.Args[1]

  blob, err := ioutil.ReadFile(fpath)
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }


  buf := &bytes.Buffer{}
  mw  := multipart.NewWriter(buf)

  fw, err := mw.CreateFormFile(formarg, path.Base(fpath))
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }

  _, err = fw.Write(blob)
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }

  err = mw.Close()
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }


  res, err := http.Post(endpoint, mw.FormDataContentType(), buf)
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }

  defer res.Body.Close()

  result, err := ioutil.ReadAll(res.Body)
  if err != nil {
    fmt.Fprintln(os.Stderr, err.Error())
    os.Exit(1)
  }

  fmt.Printf("%s", result)
}
