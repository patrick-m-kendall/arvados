// Copyright (C) The Arvados Authors. All rights reserved.
//
// SPDX-License-Identifier: Apache-2.0

package arvados

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"syscall"
	"time"

	check "gopkg.in/check.v1"
)

const (
	// Importing arvadostest would be an import cycle, so these
	// fixtures are duplicated here [until fs moves to a separate
	// package].
	fixtureActiveToken                  = "3kg6k6lzmp9kj5cpkcoxie963cmvjahbt2fod9zru30k1jqdmi"
	fixtureAProjectUUID                 = "zzzzz-j7d0g-v955i6s2oi1cbso"
	fixtureThisFilterGroupUUID          = "zzzzz-j7d0g-thisfiltergroup"
	fixtureAFilterGroupTwoUUID          = "zzzzz-j7d0g-afiltergrouptwo"
	fixtureAFilterGroupThreeUUID        = "zzzzz-j7d0g-filtergroupthre"
	fixtureAFilterGroupFourUUID         = "zzzzz-j7d0g-filtergroupfour"
	fixtureAFilterGroupFiveUUID         = "zzzzz-j7d0g-filtergroupfive"
	fixtureFooAndBarFilesInDirUUID      = "zzzzz-4zz18-foonbarfilesdir"
	fixtureFooCollectionName            = "zzzzz-4zz18-fy296fx3hot09f7 added sometime"
	fixtureFooCollectionPDH             = "1f4b0bc7583c2a7f9102c395f4ffc5e3+45"
	fixtureFooCollection                = "zzzzz-4zz18-fy296fx3hot09f7"
	fixtureNonexistentCollection        = "zzzzz-4zz18-totallynotexist"
	fixtureStorageClassesDesiredArchive = "zzzzz-4zz18-3t236wr12769qqa"
	fixtureBlobSigningKey               = "zfhgfenhffzltr9dixws36j1yhksjoll2grmku38mi7yxd66h5j4q9w4jzanezacp8s6q0ro3hxakfye02152hncy6zml2ed0uc"
	fixtureBlobSigningTTL               = 336 * time.Hour
)

var _ = check.Suite(&SiteFSSuite{})

func init() {
	// Enable DebugLocksPanicMode sometimes. Don't enable it all
	// the time, though -- it adds many calls to time.Sleep(),
	// which could hide different bugs.
	if time.Now().Second()&1 == 0 {
		DebugLocksPanicMode = true
	}
}

type SiteFSSuite struct {
	client *Client
	fs     CustomFileSystem
	kc     keepClient
}

func (s *SiteFSSuite) SetUpTest(c *check.C) {
	s.client = &Client{
		APIHost:   os.Getenv("ARVADOS_API_HOST"),
		AuthToken: fixtureActiveToken,
		Insecure:  true,
	}
	s.kc = &keepClientStub{
		blocks: map[string][]byte{
			"3858f62230ac3c915f300c664312c63f": []byte("foobar"),
		},
		sigkey:    fixtureBlobSigningKey,
		sigttl:    fixtureBlobSigningTTL,
		authToken: fixtureActiveToken,
	}
	s.fs = s.client.SiteFileSystem(s.kc)
}

func (s *SiteFSSuite) TestHttpFileSystemInterface(c *check.C) {
	_, ok := s.fs.(http.FileSystem)
	c.Check(ok, check.Equals, true)
}

func (s *SiteFSSuite) TestByIDEmpty(c *check.C) {
	f, err := s.fs.Open("/by_id")
	c.Assert(err, check.IsNil)
	fis, err := f.Readdir(-1)
	c.Check(err, check.IsNil)
	c.Check(len(fis), check.Equals, 0)
}

func (s *SiteFSSuite) TestUpdateStorageClasses(c *check.C) {
	f, err := s.fs.OpenFile("/by_id/"+fixtureStorageClassesDesiredArchive+"/newfile", os.O_CREATE|os.O_RDWR, 0777)
	c.Assert(err, check.IsNil)
	_, err = f.Write([]byte("nope"))
	c.Assert(err, check.IsNil)
	err = f.Close()
	c.Assert(err, check.IsNil)
	err = s.fs.Sync()
	c.Assert(err, check.ErrorMatches, `.*stub does not write storage class "archive"`)
}

func (s *SiteFSSuite) TestByUUIDAndPDH(c *check.C) {
	f, err := s.fs.Open("/by_id")
	c.Assert(err, check.IsNil)
	fis, err := f.Readdir(-1)
	c.Check(err, check.IsNil)
	c.Check(len(fis), check.Equals, 0)

	err = s.fs.Mkdir("/by_id/"+fixtureFooCollection, 0755)
	c.Check(err, check.Equals, os.ErrExist)

	f, err = s.fs.Open("/by_id/" + fixtureNonexistentCollection)
	c.Assert(err, check.Equals, os.ErrNotExist)

	for _, path := range []string{
		fixtureFooCollection,
		fixtureFooCollectionPDH,
		fixtureAProjectUUID + "/" + fixtureFooCollectionName,
	} {
		f, err = s.fs.Open("/by_id/" + path)
		c.Assert(err, check.IsNil)
		fis, err = f.Readdir(-1)
		c.Assert(err, check.IsNil)
		var names []string
		for _, fi := range fis {
			names = append(names, fi.Name())
		}
		c.Check(names, check.DeepEquals, []string{"foo"})
	}

	f, err = s.fs.Open("/by_id/" + fixtureAProjectUUID + "/A Subproject/baz_file")
	c.Assert(err, check.IsNil)
	fis, err = f.Readdir(-1)
	c.Assert(err, check.IsNil)
	var names []string
	for _, fi := range fis {
		names = append(names, fi.Name())
	}
	c.Check(names, check.DeepEquals, []string{"baz"})

	_, err = s.fs.OpenFile("/by_id/"+fixtureNonexistentCollection, os.O_RDWR|os.O_CREATE, 0755)
	c.Check(err, ErrorIs, ErrInvalidOperation)
	err = s.fs.Rename("/by_id/"+fixtureFooCollection, "/by_id/beep")
	c.Check(err, ErrorIs, ErrInvalidOperation)
	err = s.fs.Rename("/by_id/"+fixtureFooCollection+"/foo", "/by_id/beep")
	c.Check(err, ErrorIs, ErrInvalidOperation)
	_, err = s.fs.Stat("/by_id/beep")
	c.Check(err, check.Equals, os.ErrNotExist)
	err = s.fs.Rename("/by_id/"+fixtureFooCollection+"/foo", "/by_id/"+fixtureFooCollection+"/bar")
	c.Check(err, check.IsNil)

	err = s.fs.Rename("/by_id", "/beep")
	c.Check(err, ErrorIs, ErrInvalidOperation)
}

// Copy subtree from OS src to dst path inside fs. If src is a
// directory, dst must exist and be a directory.
func copyFromOS(fs FileSystem, dst, src string) error {
	inf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer inf.Close()
	dirents, err := inf.Readdir(-1)
	if e, ok := err.(*os.PathError); ok {
		if e, ok := e.Err.(syscall.Errno); ok {
			if e == syscall.ENOTDIR {
				err = syscall.ENOTDIR
			}
		}
	}
	if err == syscall.ENOTDIR {
		outf, err := fs.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_TRUNC|os.O_WRONLY, 0700)
		if err != nil {
			return fmt.Errorf("open %s: %s", dst, err)
		}
		defer outf.Close()
		_, err = io.Copy(outf, inf)
		if err != nil {
			return fmt.Errorf("%s: copying data from %s: %s", dst, src, err)
		}
		err = outf.Close()
		if err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("%s: readdir: %T %s", src, err, err)
	} else {
		{
			d, err := fs.Open(dst)
			if err != nil {
				return fmt.Errorf("opendir(%s): %s", dst, err)
			}
			d.Close()
		}
		for _, ent := range dirents {
			if ent.Name() == "." || ent.Name() == ".." {
				continue
			}
			dstname := dst + "/" + ent.Name()
			if ent.IsDir() {
				err = fs.Mkdir(dstname, 0700)
				if err != nil {
					return fmt.Errorf("mkdir %s: %s", dstname, err)
				}
			}
			err = copyFromOS(fs, dstname, src+"/"+ent.Name())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SiteFSSuite) TestSnapshotSplice(c *check.C) {
	s.fs.MountProject("home", "")
	thisfile, err := ioutil.ReadFile("fs_site_test.go")
	c.Assert(err, check.IsNil)

	var src1 Collection
	err = s.client.RequestAndDecode(&src1, "POST", "arvados/v1/collections", nil, map[string]interface{}{
		"collection": map[string]string{
			"name":       "TestSnapshotSplice src1",
			"owner_uuid": fixtureAProjectUUID,
		},
	})
	c.Assert(err, check.IsNil)
	defer s.client.RequestAndDecode(nil, "DELETE", "arvados/v1/collections/"+src1.UUID, nil, nil)
	err = s.fs.Sync()
	c.Assert(err, check.IsNil)
	err = copyFromOS(s.fs, "/home/A Project/TestSnapshotSplice src1", "..") // arvados.git/sdk/go
	c.Assert(err, check.IsNil)

	var src2 Collection
	err = s.client.RequestAndDecode(&src2, "POST", "arvados/v1/collections", nil, map[string]interface{}{
		"collection": map[string]string{
			"name":       "TestSnapshotSplice src2",
			"owner_uuid": fixtureAProjectUUID,
		},
	})
	c.Assert(err, check.IsNil)
	defer s.client.RequestAndDecode(nil, "DELETE", "arvados/v1/collections/"+src2.UUID, nil, nil)
	err = s.fs.Sync()
	c.Assert(err, check.IsNil)
	err = copyFromOS(s.fs, "/home/A Project/TestSnapshotSplice src2", "..") // arvados.git/sdk/go
	c.Assert(err, check.IsNil)

	var dst Collection
	err = s.client.RequestAndDecode(&dst, "POST", "arvados/v1/collections", nil, map[string]interface{}{
		"collection": map[string]string{
			"name":       "TestSnapshotSplice dst",
			"owner_uuid": fixtureAProjectUUID,
		},
	})
	c.Assert(err, check.IsNil)
	defer s.client.RequestAndDecode(nil, "DELETE", "arvados/v1/collections/"+dst.UUID, nil, nil)
	err = s.fs.Sync()
	c.Assert(err, check.IsNil)

	dstPath := "/home/A Project/TestSnapshotSplice dst"
	err = copyFromOS(s.fs, dstPath, "..") // arvados.git/sdk/go
	c.Assert(err, check.IsNil)

	// Snapshot directory
	snap1, err := Snapshot(s.fs, "/home/A Project/TestSnapshotSplice src1/ctxlog")
	c.Check(err, check.IsNil)
	// Attach same snapshot twice, at paths that didn't exist before
	err = Splice(s.fs, dstPath+"/ctxlog-copy", snap1)
	c.Check(err, check.IsNil)
	err = Splice(s.fs, dstPath+"/ctxlog-copy2", snap1)
	c.Check(err, check.IsNil)
	// Splicing a snapshot twice results in two independent copies
	err = s.fs.Rename(dstPath+"/ctxlog-copy2/log.go", dstPath+"/ctxlog-copy/log2.go")
	c.Check(err, check.IsNil)
	_, err = s.fs.Open(dstPath + "/ctxlog-copy2/log.go")
	c.Check(err, check.Equals, os.ErrNotExist)
	f, err := s.fs.Open(dstPath + "/ctxlog-copy/log.go")
	if c.Check(err, check.IsNil) {
		buf, err := ioutil.ReadAll(f)
		c.Check(err, check.IsNil)
		c.Check(string(buf), check.Not(check.Equals), "")
		f.Close()
	}

	// Snapshot regular file
	snapFile, err := Snapshot(s.fs, "/home/A Project/TestSnapshotSplice src1/arvados/fs_site_test.go")
	c.Check(err, check.IsNil)
	// Replace dir with file
	err = Splice(s.fs, dstPath+"/ctxlog-copy2", snapFile)
	c.Check(err, check.IsNil)
	if f, err := s.fs.Open(dstPath + "/ctxlog-copy2"); c.Check(err, check.IsNil) {
		buf, err := ioutil.ReadAll(f)
		c.Check(err, check.IsNil)
		c.Check(string(buf), check.Equals, string(thisfile))
	}

	// Cannot splice a file onto a collection root, or anywhere
	// outside a collection
	for _, badpath := range []string{
		dstPath,
		"/home/A Project/newnodename",
		"/home/A Project",
		"/home/newnodename",
		"/home",
		"/newnodename",
	} {
		err = Splice(s.fs, badpath, snapFile)
		c.Check(err, check.NotNil)
		c.Check(err, ErrorIs, ErrInvalidOperation, check.Commentf("badpath %s"))
		if badpath == dstPath {
			c.Check(err, check.ErrorMatches, `cannot use Splice to attach a file at top level of \*arvados.collectionFileSystem: invalid operation`, check.Commentf("badpath: %s", badpath))
			continue
		}
		err = Splice(s.fs, badpath, snap1)
		c.Check(err, ErrorIs, ErrInvalidOperation, check.Commentf("badpath %s"))
	}

	// Destination cannot have trailing slash
	for _, badpath := range []string{
		dstPath + "/ctxlog/",
		dstPath + "/",
		"/home/A Project/",
		"/home/",
		"/",
		"",
	} {
		err = Splice(s.fs, badpath, snap1)
		c.Check(err, ErrorIs, ErrInvalidArgument, check.Commentf("badpath %s", badpath))
		err = Splice(s.fs, badpath, snapFile)
		c.Check(err, ErrorIs, ErrInvalidArgument, check.Commentf("badpath %s", badpath))
	}

	// Destination's parent must already exist
	for _, badpath := range []string{
		dstPath + "/newdirname/",
		dstPath + "/newdirname/foobar",
		"/foo/bar",
	} {
		err = Splice(s.fs, badpath, snap1)
		c.Check(err, ErrorIs, os.ErrNotExist, check.Commentf("badpath %s", badpath))
		err = Splice(s.fs, badpath, snapFile)
		c.Check(err, ErrorIs, os.ErrNotExist, check.Commentf("badpath %s", badpath))
	}

	snap2, err := Snapshot(s.fs, dstPath+"/ctxlog-copy")
	c.Check(err, check.IsNil)
	err = Splice(s.fs, dstPath+"/ctxlog-copy-copy", snap2)
	c.Check(err, check.IsNil)

	// Snapshot entire collection, splice into same collection at
	// a new path, remove file from original location, verify
	// spliced content survives
	snapDst, err := Snapshot(s.fs, dstPath+"")
	c.Check(err, check.IsNil)
	err = Splice(s.fs, dstPath+"", snapDst)
	c.Check(err, check.IsNil)
	err = Splice(s.fs, dstPath+"/copy1", snapDst)
	c.Check(err, check.IsNil)
	err = Splice(s.fs, dstPath+"/copy2", snapDst)
	c.Check(err, check.IsNil)
	err = s.fs.RemoveAll(dstPath + "/arvados/fs_site_test.go")
	c.Check(err, check.IsNil)
	err = s.fs.RemoveAll(dstPath + "/arvados")
	c.Check(err, check.IsNil)
	_, err = s.fs.Open(dstPath + "/arvados/fs_site_test.go")
	c.Check(err, check.Equals, os.ErrNotExist)
	f, err = s.fs.Open(dstPath + "/copy2/arvados/fs_site_test.go")
	c.Check(err, check.IsNil)
	defer f.Close()
	buf, err := ioutil.ReadAll(f)
	c.Check(err, check.IsNil)
	c.Check(string(buf), check.Equals, string(thisfile))
}
