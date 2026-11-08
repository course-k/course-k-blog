import copy
from pathlib import Path
import tempfile
import unittest
from delivery import ReleaseError, schema_digest
from production import production_template
from special_release import review_template, review_special_changes
from check_release_revision import passed, REQUIRED

class SpecialReleaseTest(unittest.TestCase):
    def test_only_migration_image_and_staging_settings_can_change(self):
        target=production_template();actual=copy.deepcopy(target)
        actual['Parameters'].pop('MigrationImage');actual['Conditions'].pop('MigrationOverride')
        actual['Resources']['MigrationTask']['Properties']['ContainerDefinitions'][0]['Image']={'Ref':'GoImage'}
        actual['Resources']['ImagesBucket']['Properties'].pop('CorsConfiguration')
        actual['Resources']['ImagesBucket']['Properties'].pop('LifecycleConfiguration')
        self.assertEqual(set(review_template(actual,target)),{'MigrationTask','ImagesBucket'})
        for section,key,value in [('Conditions','RunApplication',{'Fn::Equals':['false','true']}),('Resources','Database',{})]:
            altered=copy.deepcopy(target);altered[section][key]=value
            with self.assertRaises(ReleaseError):review_template(actual,altered)
        altered=copy.deepcopy(target);altered['Resources']['ImagesBucket']['Properties']['PublicAccessBlockConfiguration']['BlockPublicPolicy']=False
        with self.assertRaises(ReleaseError):review_template(actual,altered)
    def test_bucket_replacement_and_unexpected_dependent_mutation_stop(self):
        def item(name,**kw):return {'ResourceChange':{'LogicalResourceId':name,'Action':'Modify','Replacement':'False','Details':[],**kw}}
        review_special_changes([item('ImagesBucket')])
        for c in [item('Database'),{'ResourceChange':{'LogicalResourceId':'ImagesBucket','Action':'Modify','Replacement':'True'}},item('ApplicationTaskRole', Details=[{'Evaluation':'Static','Target':{'Name':'Policies'}}])]:
            with self.assertRaises(ReleaseError):review_special_changes([c])
    def test_schema_digest_changes_on_sql_and_migration_engine_changes(self):
        with tempfile.TemporaryDirectory() as folder:
            root=Path(folder);p=root/'backend/internal/mysql';(p/'migrations').mkdir(parents=True)
            (p/'migrations.go').write_text('engine');(p/'migrations/001_initial.sql').write_text('first')
            a=schema_digest(root);(p/'migrations/001_initial.sql').write_text('second');b=schema_digest(root)
            self.assertNotEqual(a,b);(p/'migrations.go').write_text('new engine');self.assertNotEqual(b,schema_digest(root))
    def test_exact_main_required_jobs_and_latest_attempt(self):
        jobs=[{'id':i,'name':n,'conclusion':'success'} for i,n in enumerate(sorted(REQUIRED))]
        self.assertTrue(passed(jobs));self.assertFalse(passed(jobs[:-1]))
        jobs.append({'id':99,'name':jobs[0]['name'],'conclusion':'failure'});self.assertFalse(passed(jobs))
