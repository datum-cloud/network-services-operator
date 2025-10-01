package gatewayapi

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"maps"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Wrap a fs.FS, attach tweaks to file names that are provided file contents,
// and return the desired file contents. Useful for adding labels to resource,
// or mutating upstream manifests so they work within NSO constraints.

const wildcard = "*"

type TweakedFS struct {
	fs             fs.FS
	tweaks         map[string]map[string]func(*unstructured.Unstructured)
	yamlSerializer *json.Serializer
}

func NewTweakedFS(fsys fs.FS) *TweakedFS {
	return &TweakedFS{
		fs: fsys,
		tweaks: map[string]map[string]func(*unstructured.Unstructured){
			wildcard: {},
		},
	}
}

func (f *TweakedFS) InitYAMLSerializer(scheme *runtime.Scheme) {
	f.yamlSerializer = json.NewYAMLSerializer(
		json.DefaultMetaFactory, scheme, scheme,
	)
}

func (f *TweakedFS) AddTweak(fileName, groupVersion, kind, namespace, name string, tweak func(*unstructured.Unstructured)) {
	if _, ok := f.tweaks[fileName]; !ok {
		f.tweaks[fileName] = make(map[string]func(*unstructured.Unstructured))
	}
	key := groupVersion
	if !strings.Contains(groupVersion, "/") {
		key = "/" + key
	}

	if kind != wildcard {
		key += "/" + kind
	}
	if namespace != wildcard {
		key += "/" + namespace
	}
	if name != wildcard {
		key += "/" + name
	}

	f.tweaks[fileName][key] = tweak
}

func (f *TweakedFS) Open(name string) (fs.File, error) {
	file, err := f.fs.Open(name)
	if err != nil {
		return nil, err
	}

	content, err := fs.ReadFile(f.fs, name)
	if err != nil {
		return nil, err
	}

	tweaks := f.tweaks[wildcard]
	if specificTweaks, ok := f.tweaks[name]; ok {
		maps.Insert(tweaks, maps.All(specificTweaks))
	}

	if len(tweaks) > 0 {
		decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(content), 4096)

		var resources []string
		for {
			uObj := unstructured.Unstructured{}
			if err := decoder.Decode(&uObj); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, err
			}
			if len(uObj.Object) == 0 {
				continue
			}

			gvk := uObj.GroupVersionKind()
			key := gvk.Group + "/" + gvk.Version + "/" + gvk.Kind + "/" + uObj.GetNamespace() + "/" + uObj.GetName()
			for k, tweak := range tweaks {
				if strings.HasPrefix(key, k) {
					tweak(&uObj)
				}
			}

			resources = append(resources, runtime.EncodeOrDie(f.yamlSerializer, &uObj))
		}

		content = []byte(strings.Join(resources, "---\n"))
	}

	return &TweakedFile{
		File:    file,
		content: content,
		offset:  0,
	}, nil
}

type TweakedFile struct {
	fs.File
	content []byte
	offset  int64
}

func (f *TweakedFile) Read(p []byte) (int, error) {
	if f.offset >= int64(len(f.content)) {
		return 0, io.EOF
	}

	n := copy(p, f.content[f.offset:])
	f.offset += int64(n)

	if f.offset >= int64(len(f.content)) {
		return n, io.EOF
	}

	return n, nil
}

func (f *TweakedFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}

	return TweakedFileInfo{
		FileInfo: info,
		size:     int64(len(f.content)),
	}, nil
}

type TweakedFileInfo struct {
	fs.FileInfo
	size int64
}

func (f TweakedFileInfo) Size() int64 {
	return f.size
}
