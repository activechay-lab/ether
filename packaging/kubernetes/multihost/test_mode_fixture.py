"""Offline contracts for fixture byte preservation and identity/read isolation."""
import copy
import hashlib
import http.client
import importlib.util
import json
from pathlib import Path
import socket
import tempfile
import threading
import unittest


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


mode = load('mode_fixture_contract', 'mode-fixture.py')
resources = load('mode_fixture_resource_contract', 'mode-fixture-resources.py')
seed = load('mode_fixture_seed_contract', 'seed-mode-titles.py')


class ModeContracts(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = bytes(range(256))*1025
        primary_path = self.root/'fixture.mp4'
        primary_path.write_bytes(self.data)
        self.primary = mode.base.Fixture(primary_path, 'http://fixture.invalid:8080', 900001,
                                         'Ether.Cluster.Fixture.2020.360p.WEB-DL.MPEG4.AAC')
        self.manifest = {'version':1, 'namespace':'ether035-ha', 'profiles':[]}
        self.files = []
        for slug, tmdb in mode.PROFILES.items():
            filename = slug+'.bin'
            (self.root/filename).write_bytes(self.data)
            p = {'id':slug, 'tmdb':tmdb, 'ratingKey':str(10**15+tmdb), 'file':filename,
                 'fixtureId':'ether035-ha-'+slug+'-fixture', 'articlePrefix':'ether035-ha-'+slug+'-part',
                 'sha256':hashlib.sha256(self.data).hexdigest(), 'bytes':len(self.data),
                 'title':'Generated '+slug, 'release':'Generated.Fixture.2020.WEB-DL.H264.AAC'}
            self.manifest['profiles'].append(p)
            self.files.append(mode.FileFixture(self.root, self.primary.origin, p))
        self.registry = mode.Registry(self.primary, self.files)

    def test_default_bytes_and_bounded_file_articles(self):
        # Captured from the unmodified6b fixture encoder, including escaping,
        # first/final multipart CRCs and the original Message-ID namespace.
        self.assertEqual(hashlib.sha256(self.primary.nzb()).hexdigest(),
                         'b9761cb8e2bf3b0f4b1318de0162c184427b328a1e31aee052f28fbabeff6da2')
        expected = ['6ce73d3ae2d02fc31a754642c864a176c76e2ea22f0d26e315e2b698dd058194',
                    '7b91b7825c5dc4a4048decb0fbf917442bc3c1ccc9feb2ff10d3ba49bf14336c']
        for i, digest in enumerate(expected):
            self.assertEqual(hashlib.sha256(self.primary.encode(i)).hexdigest(), digest)
        for f in self.files:
            self.assertFalse(hasattr(f, 'data'))
            self.assertFalse(hasattr(f, 'chunks'))
            memory = mode.base.Fixture(f.path, f.origin, f.tmdb, f.release, f.fixture_id, f.article_prefix)
            self.assertEqual(f.nzb(), memory.nzb())
            for i in range(f.parts):
                self.assertEqual(f.encode(i), memory.encode(i))
            self.assertIsNone(f.articles.get(f.article_prefix+'-00000@fixture.invalid'))
            self.assertIsNone(f.articles.get(f.article_prefix+'-99999@fixture.invalid'))
            self.assertIsNone(f.articles.get(self.primary.article_prefix+'-00001@fixture.invalid'))

    def test_request_and_same_connection_article_isolation(self):
        self.assertIs(self.registry.request_fixture({}), self.primary)
        self.assertIs(self.registry.request_fixture({'tmdbid':['900002']}), self.primary)
        for f in self.files:
            self.assertIs(self.registry.request_fixture({'tmdbid':[str(f.tmdb)]}), f)
            self.assertIs(self.registry.request_fixture({'t':['get'], 'id':[f.fixture_id]}), f)
        news = mode.News(('127.0.0.1', 0), self.registry)
        thread = threading.Thread(target=news.serve_forever)
        thread.start()
        try:
            with socket.create_connection(news.server_address, timeout=2) as connection:
                stream = connection.makefile('rwb', buffering=0)
                self.assertTrue(stream.readline().startswith(b'200'))
                for f in [self.files[0], self.files[1], self.primary, self.files[2]]:
                    article = f.article_prefix+'-00001@fixture.invalid'
                    stream.write(('HEAD <'+article+'>\r\n').encode())
                    self.assertTrue(stream.readline().startswith(b'221'))
                    lines = []
                    while (line := stream.readline()) != b'.\r\n':
                        self.assertTrue(line)
                        lines.append(line)
                    self.assertIn(('Subject: '+f.filename+'\r\n').encode(), lines)
                stream.write(b'QUIT\r\n')
                self.assertTrue(stream.readline().startswith(b'205'))
                stream.close()
        finally:
            news.shutdown(); news.server_close(); thread.join()

    def test_http_ranges_and_immutable_open(self):
        web = mode.Web(('127.0.0.1', 0), self.registry)
        thread = threading.Thread(target=web.serve_forever)
        thread.start()
        try:
            for value, status, body in [('bytes=7-19',206,self.data[7:20]),
                                        ('bytes=-9',206,self.data[-9:]),
                                        ('bytes=0-2,8-9',416,b''),
                                        ('bytes=999999-',416,b'')]:
                c = http.client.HTTPConnection(*web.server_address, timeout=2)
                c.request('GET', '/media/ramp96', headers={'Range':value})
                response = c.getresponse()
                self.assertEqual(response.status, status)
                self.assertEqual(response.read(), body)
                c.close()
            c = http.client.HTTPConnection(*web.server_address, timeout=2)
            c.request('HEAD', '/media/copy96')
            response = c.getresponse()
            self.assertEqual(response.status, 200)
            self.assertEqual(int(response.getheader('Content-Length')), len(self.data))
            self.assertEqual(response.read(), b'')
            c.close()
            self.files[0].path.write_bytes(b'changed')
            with self.assertRaises(ValueError):
                self.files[0].open()
        finally:
            web.shutdown(); web.server_close(); thread.join()

    def test_manifest_scope_and_title_only_collision_sql(self):
        path = self.root/'profiles.json'
        path.write_text(json.dumps(self.manifest))
        self.assertEqual(mode.read_manifest(path, 'ether035-ha'), self.manifest)
        bad = copy.deepcopy(self.manifest)
        bad['profiles'][0]['articlePrefix'] = 'ether035-lab2-ramp96-part'
        path.write_text(json.dumps(bad))
        with self.assertRaises(ValueError):
            mode.read_manifest(path, 'ether035-ha')
        sql = seed.seed_sql(self.manifest, 1)
        self.assertEqual(sql.count('INSERT INTO catalog_titles('), 3)
        self.assertIn('RAISE EXCEPTION', sql)
        for forbidden in ['UPDATE ', 'auth_', 'placement_', 'SourceRevision', 'CopyTimeline', 'streams']:
            self.assertNotIn(forbidden, sql)

    def test_capacity_and_byte_preserving_rollback(self):
        ns = 'ether035-ha'
        deployment = {'metadata':{'namespace':ns,'name':ns+'-source','uid':'fixture-uid','resourceVersion':'9'},
                      'spec':{'replicas':1,'template':{'metadata':{'labels':{'app':ns+'-source'}},'spec':{
                          'nodeSelector':{'kubernetes.io/hostname':'ether035-mh-server'},
                          'securityContext':{'runAsUser':999,'runAsGroup':999},
                          'initContainers':[{'name':'generate'}],
                          'containers':[{'name':'source','image':'python:3.12.12-slim-bookworm',
                                         'securityContext':{'readOnlyRootFilesystem':True},
                                         'args':['--media','/media/fixture.mp4','--origin','http://'+ns+'-source.'+ns+'.svc.cluster.local:8080']}],
                          'volumes':[{'name':'media','emptyDir':{'sizeLimit':'100Mi'}},
                                     {'name':'code','configMap':{'name':ns+'-source-code'}}]}}}}
        result = resources.render(deployment, self.manifest, 32506600)
        rollback = result['rollbackPodTemplate']['spec']
        self.assertNotIn('initContainers', rollback)
        self.assertEqual(rollback['volumes'][1]['configMap']['name'], ns+'-source-code')
        self.assertTrue(rollback['volumes'][0]['persistentVolumeClaim']['readOnly'])
        self.assertEqual(result['deploymentJSONPatch'][0]['value'], 'fixture-uid')
        self.assertIn('initContainers', deployment['spec']['template']['spec'])
        with self.assertRaises(ValueError):
            resources.render(deployment, self.manifest, 128*1024*1024)


if __name__ == '__main__':
    unittest.main()
