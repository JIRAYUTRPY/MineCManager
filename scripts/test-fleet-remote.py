#!/usr/bin/env python3
"""Exercise the shipped remote program in isolated directories; never mutate the host OS."""
import base64, importlib.util, json, pathlib, pwd, tempfile, unittest
from unittest.mock import patch
ROOT=pathlib.Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('remote',ROOT/'internal/fleet/remote.py');remote=importlib.util.module_from_spec(spec);spec.loader.exec_module(remote)
class RemoteTests(unittest.TestCase):
 def setUp(self):
  self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup);self.base=pathlib.Path(self.temp.name)
  self.patches=[patch.object(remote,'BASE',self.base/'servers'),patch.object(remote,'STATE',self.base/'state')]
  for p in self.patches:p.start();self.addCleanup(p.stop)
  remote.BASE.mkdir();remote.STATE.mkdir()
  for d in ['servers','file-history','backups']:(remote.STATE/d).mkdir()
 def test_file_stale_write_and_rollback_copy(self):
  p=self.base/'config';p.write_text('before');r=remote.file_action({'action':'read','path':str(p)})
  q={'action':'write','path':str(p),'confirm':str(p),'sha256':r['sha256'],'content':base64.b64encode(b'after').decode()}
  out=remote.file_action(q);self.assertEqual(p.read_text(),'after');self.assertEqual(pathlib.Path(out['previous_copy']).read_text(),'before')
  with self.assertRaises(ValueError):remote.file_action(q)
 def test_symlink_write_rejected(self):
  p=self.base/'file';p.write_text('original');link=self.base/'link';link.symlink_to(p)
  with self.assertRaises(ValueError):remote.file_action({'action':'write','path':str(link),'confirm':str(link)})
 def test_backup_restore_and_restart(self):
  root=remote.BASE/'survival';root.mkdir();(root/'world').mkdir();(root/'world'/'level.dat').write_bytes(b'world snapshot')
  (remote.STATE/'servers'/'survival.json').write_text(json.dumps({'path':str(root)}))
  calls=[]
  with patch.object(remote,'active',side_effect=[True,False]),patch.object(remote,'run',side_effect=lambda *a,**k:calls.append(a)):
   result=remote.backup({'server':'survival','retain':2})
  self.assertEqual(calls,[('systemctl','stop','mcm-survival.service'),('systemctl','start','mcm-survival.service')])
  (root/'world'/'level.dat').write_bytes(b'changed')
  with patch.object(remote,'active',return_value=False),patch.object(remote.pwd,'getpwnam',return_value=pwd.getpwuid(remote.os.getuid())),patch.object(remote.os,'chown'):
   restored=remote.restore({'server':'survival','backup':result['backup'],'confirm':'survival'})
  self.assertEqual((root/'world'/'level.dat').read_bytes(),b'world snapshot');self.assertTrue(pathlib.Path(restored['previous_root']).exists())
  target=remote.STATE/'backups'/'survival'/result['backup'];target.write_bytes(b'corrupt')
  with patch.object(remote,'active',return_value=False),self.assertRaisesRegex(ValueError,'checksum'):remote.restore({'server':'survival','backup':result['backup'],'confirm':'survival'})
 def test_backup_failure_restarts_server(self):
  root=remote.BASE/'survival';root.mkdir();(root/'link').symlink_to('/etc/passwd');(remote.STATE/'servers'/'survival.json').write_text(json.dumps({'path':str(root)}));calls=[]
  with patch.object(remote,'active',side_effect=[True,False]),patch.object(remote,'run',side_effect=lambda *a,**k:calls.append(a)),self.assertRaises(ValueError):remote.backup({'server':'survival'})
  self.assertEqual(calls[-1],('systemctl','start','mcm-survival.service'))
 def test_invalid_deploy_has_no_os_side_effects(self):
  with patch.object(remote,'run') as run,self.assertRaises(ValueError):remote.deploy({'server':'bad;id','port':25565,'ram_mb':2048,'eula':True})
  run.assert_not_called()
 def test_command_output_limit(self):
  with self.assertRaisesRegex(ValueError,'output exceeds'):remote.run('python3','-c','print("x"*1100000)')
 def test_explicit_root_confirmation(self):
  with patch.object(remote.os,'geteuid',return_value=0),self.assertRaises(ValueError):remote.main({'action':'command','command':'id'})
if __name__=='__main__':unittest.main()
