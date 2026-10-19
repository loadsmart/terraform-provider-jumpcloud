package client

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// ApplicationLogoURL returns the URL of the application's logo, which is the
// template's default icon until a logo is uploaded.
func (c *Client) ApplicationLogoURL(ctx context.Context, id string) (string, error) {
	app, err := get[struct {
		Logo struct {
			URL string `json:"url"`
		} `json:"logo"`
	}](ctx, c, "/api/applications/"+url.PathEscape(id))
	if err != nil {
		return "", err
	}
	return app.Logo.URL, nil
}

// IsUploadedLogo reports whether url is an uploaded logo rather than a template icon.
// JumpCloud serves uploads from assets.jumpcloud.com/images/applications/.
func IsUploadedLogo(url string) bool {
	return strings.Contains(url, "/images/applications/")
}

// UploadApplicationLogo replaces the application's logo with image, a PNG or JPEG file.
func (c *Client) UploadApplicationLogo(ctx context.Context, id, filename string, image []byte) error {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, err := form.CreatePart(textproto.MIMEHeader{
		"Content-Disposition": {`form-data; name="image"; filename="` + strings.ReplaceAll(filename, `"`, "") + `"`},
		"Content-Type":        {http.DetectContentType(image)},
	})
	if err != nil {
		return err
	}
	if _, err := part.Write(image); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, logoPath(id), nil, rawBody{contentType: form.FormDataContentType(), data: buf.Bytes()}, nil)
}

// DeleteApplicationLogo puts back the template's default icon.
func (c *Client) DeleteApplicationLogo(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, logoPath(id), nil, nil, nil)
}

func logoPath(id string) string {
	return "/api/v2/applications/" + url.PathEscape(id) + "/logo"
}
