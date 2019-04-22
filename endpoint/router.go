/*
 * Copyright (c) 2019. Abstrium SAS <team (at) pydio.com>
 * This file is part of Pydio Cells.
 *
 * Pydio Cells is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * Pydio Cells is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with Pydio Cells.  If not, see <http://www.gnu.org/licenses/>.
 *
 * The latest code can be found at <https://pydio.com>.
 */

package endpoint

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/micro/go-micro/errors"

	"github.com/pydio/cells/common"
	"github.com/pydio/cells/common/log"
	natsbroker "github.com/pydio/cells/common/micro/broker/nats"
	natsregistry "github.com/pydio/cells/common/micro/registry/nats"
	grpctransport "github.com/pydio/cells/common/micro/transport/grpc"
	"github.com/pydio/cells/common/proto/tree"
	"github.com/pydio/cells/common/sync/model"
	"github.com/pydio/cells/common/views"
)

func init() {
	natsregistry.Enable()
	natsbroker.Enable()
	grpctransport.Enable()

}

type NoopWriter struct{}

func (nw *NoopWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func (nw *NoopWriter) Close() error {
	return nil
}

type RouterEndpoint struct {
	router *views.Router
	root   string
	ctx    context.Context
}

func NewRouterEndpoint(root string) *RouterEndpoint {
	return &RouterEndpoint{
		root: root,
	}
}

func (r *RouterEndpoint) LoadNode(ctx context.Context, path string, leaf ...bool) (node *tree.Node, err error) {
	resp, e := r.getRouter().ReadNode(r.getContext(ctx), &tree.ReadNodeRequest{Node: &tree.Node{Path: r.rooted(path)}})
	if e != nil {
		return nil, e
	}
	out := resp.Node
	out.Path = r.unrooted(resp.Node.Path)
	return out, nil
}

func (r *RouterEndpoint) GetEndpointInfo() model.EndpointInfo {
	return model.EndpointInfo{
		URI: "router://" + r.root,
		RequiresNormalization: false,
		RequiresFoldersRescan: false,
	}
}

func (r *RouterEndpoint) Walk(walknFc model.WalkNodesFunc, pathes ...string) (err error) {
	p := ""
	if len(pathes) > 0 {
		p = pathes[0]
	}
	log.Logger(r.getContext()).Info("Walking Router on " + r.rooted(p))
	s, e := r.getRouter().ListNodes(r.getContext(), &tree.ListNodesRequest{
		Node:      &tree.Node{Path: r.rooted(p)},
		Recursive: true,
	})
	if e != nil {
		return e
	}
	defer s.Close()
	for {
		resp, e := s.Recv()
		if e != nil {
			break
		}
		n := resp.Node
		if n.Etag == common.NODE_FLAG_ETAG_TEMPORARY {
			continue
		}
		n.Path = r.unrooted(resp.Node.Path)
		if !n.IsLeaf() {
			n.Etag = "-1" // Force recomputing Etags for Folders
		}
		walknFc(n.Path, n, nil)
	}
	return
}

func (r *RouterEndpoint) Watch(recursivePath string) (*model.WatchObject, error) {
	return nil, fmt.Errorf("not.implemented")
}

func (r *RouterEndpoint) ComputeChecksum(node *tree.Node) error {
	return fmt.Errorf("not.implemented")
}

func (r *RouterEndpoint) CreateNode(ctx context.Context, node *tree.Node, updateIfExists bool) (err error) {
	n := node.Clone()
	n.Path = r.rooted(n.Path)
	n.Uuid = ""
	_, e := r.getRouter().CreateNode(r.getContext(ctx), &tree.CreateNodeRequest{Node: n})
	return e
}

func (r *RouterEndpoint) UpdateNode(ctx context.Context, node *tree.Node) (err error) {
	n := node.Clone()
	n.Path = r.rooted(n.Path)
	_, e := r.getRouter().CreateNode(r.getContext(ctx), &tree.CreateNodeRequest{Node: n})
	return e
}

func (r *RouterEndpoint) DeleteNode(ctx context.Context, name string) (err error) {
	router := r.getRouter()
	ctx = r.getContext(ctx)
	read, e := router.ReadNode(ctx, &tree.ReadNodeRequest{Node: &tree.Node{Path: r.rooted(name)}})
	if e != nil {
		log.Logger(ctx).Error("Trying to delete node with error " + r.rooted(name) + ":" + e.Error())
		if errors.Parse(e.Error()).Code == 404 {
			return nil
		} else {
			return e
		}
	}
	node := read.Node
	if node.IsLeaf() {
		log.Logger(ctx).Info("DeleteNode LEAF: " + node.Path)
		_, err = router.DeleteNode(ctx, &tree.DeleteNodeRequest{Node: node.Clone()})
	} else {
		log.Logger(ctx).Info("DeleteNode COLL: " + node.Path)
		pFile := path.Join(node.Path, common.PYDIO_SYNC_HIDDEN_FILE_META)
		// Now list all children and delete them all
		stream, err := router.ListNodes(ctx, &tree.ListNodesRequest{Node: node, Recursive: true})
		if err != nil {
			return err
		}
		defer stream.Close()
		for {
			resp, e := stream.Recv()
			if e != nil {
				break
			}
			if resp == nil {
				continue
			}
			if resp.Node.Path == pFile {
				continue
			}
			log.Logger(ctx).Info("DeleteNode List Children: " + resp.Node.Path)
			if !resp.Node.IsLeaf() {
				resp.Node.Path = path.Join(resp.Node.Path, common.PYDIO_SYNC_HIDDEN_FILE_META, "/")
				resp.Node.Type = tree.NodeType_LEAF
			}
			if _, err := router.DeleteNode(ctx, &tree.DeleteNodeRequest{Node: resp.Node}); err != nil {
				log.Logger(ctx).Error("Error while deleting node child " + err.Error())
				return err
			}
		}
		log.Logger(ctx).Info("Finally delete .pydio: " + pFile)
		_, err = router.DeleteNode(ctx, &tree.DeleteNodeRequest{Node: &tree.Node{
			Path: pFile,
			Type: tree.NodeType_LEAF,
		}})
	}
	return err
}

func (r *RouterEndpoint) MoveNode(ctx context.Context, oldPath string, newPath string) (err error) {
	if from, err := r.LoadNode(ctx, oldPath); err == nil {
		to := from.Clone()
		to.Path = r.rooted(newPath)
		from.Path = r.rooted(from.Path)
		_, e := r.getRouter().UpdateNode(r.getContext(ctx), &tree.UpdateNodeRequest{From: from, To: to})
		return e
	} else {
		return err
	}
}

func (r *RouterEndpoint) GetWriterOn(p string, targetSize int64) (out io.WriteCloser, err error) {
	if targetSize == 0 {
		return nil, fmt.Errorf("cannot create empty files")
	}
	if path.Base(p) == common.PYDIO_SYNC_HIDDEN_FILE_META {
		return &NoopWriter{}, nil
	}
	n := &tree.Node{Path: r.rooted(p)}
	reader, out := io.Pipe()
	go func() {
		_, e := r.getRouter().PutObject(r.getContext(), n, reader, &views.PutRequestData{Size: targetSize})
		if e != nil {
			fmt.Println("[ERROR]", "Cannot PutObject", e.Error())
		}
		reader.Close()
	}()
	return out, nil

}

func (r *RouterEndpoint) GetReaderOn(p string) (out io.ReadCloser, err error) {
	n := &tree.Node{Path: r.rooted(p)}
	o, e := r.getRouter().GetObject(r.getContext(), n, &views.GetRequestData{StartOffset: 0, Length: -1})
	return o, e
}

func (r *RouterEndpoint) getRouter() *views.Router {
	if r.router == nil {
		r.router = views.NewStandardRouter(views.RouterOptions{
			WatchRegistry: true,
			AdminView:     true,
			Synchronous:   true,
		})
	}
	return r.router
}

func (r *RouterEndpoint) getContext(ctx ...context.Context) context.Context {
	c := context.Background()
	if len(ctx) > 0 {
		c = ctx[0]
	}
	return context.WithValue(c, common.PYDIO_CONTEXT_USER_KEY, common.PYDIO_SYSTEM_USERNAME)
}

func (r *RouterEndpoint) rooted(p string) string {
	return path.Join(r.root, p)
}

func (r *RouterEndpoint) unrooted(p string) string {
	return strings.TrimLeft(strings.TrimPrefix(p, r.root), "/")
}
